package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"kubesage/internal/config"
	"kubesage/internal/eval"
	"kubesage/internal/k8s"
	"kubesage/internal/service"

	"go.uber.org/zap"
)

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	if len(args) == 0 {
		printUsage(os.Stderr)
		return 2
	}
	command := args[0]
	commandArgs := args[1:]
	if strings.HasPrefix(command, "-") {
		command = "run"
		commandArgs = args
	}

	var err error
	switch command {
	case "run":
		err = runEval(commandArgs)
	case "compare":
		err = runCompare(commandArgs)
	case "gen-fixture":
		err = runGenFixture(commandArgs)
	case "help", "-h", "--help":
		printUsage(os.Stdout)
		return 0
	default:
		err = fmt.Errorf("unknown subcommand %q", command)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "rca-eval:", err)
		return 1
	}
	return 0
}

func runEval(args []string) error {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	casesPath := fs.String("cases", "eval/cases", "case YAML file or directory")
	outPath := fs.String("out", "eval-results.json", "JSON output path, or - for stdout")
	summaryPath := fs.String("summary", "eval-summary.md", "Markdown summary output path")
	timeout := fs.Duration("timeout", 30*time.Second, "default per-case timeout")
	fullReport := fs.Bool("full-report", false, "include full diagnostic reports in JSON")
	enforce := fs.Bool("enforce", true, "exit non-zero when quality gates fail")
	if err := fs.Parse(args); err != nil {
		return err
	}

	result, err := eval.RunSuite(context.Background(), eval.RunnerOptions{
		CasesPath:          *casesPath,
		DefaultTimeout:     *timeout,
		IncludeFullReports: *fullReport,
	})
	if err != nil {
		return err
	}
	if err := eval.WriteJSON(*outPath, result); err != nil {
		return err
	}
	if *summaryPath != "" {
		if err := eval.WriteMarkdown(*summaryPath, result); err != nil {
			return err
		}
	}
	fmt.Fprintf(os.Stderr, "RCA eval: %d/%d passed, accuracy %.1f%%, hallucination %.1f%%, avg %dms\n",
		result.Metrics.PassedCases,
		result.Metrics.TotalCases,
		result.Metrics.RCAAccuracy*100,
		result.Metrics.HallucinationRate*100,
		result.Metrics.AverageDurationMS,
	)
	if *enforce {
		if err := enforceQualityGates(result); err != nil {
			return err
		}
	}
	return nil
}

func runCompare(args []string) error {
	fs := flag.NewFlagSet("compare", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	outPath := fs.String("out", "eval-comparison.md", "Markdown comparison output path")
	if err := fs.Parse(args); err != nil {
		return err
	}
	rest := fs.Args()
	if len(rest) != 2 {
		return errors.New("compare requires before.json and after.json")
	}
	before, err := eval.ReadJSON(rest[0])
	if err != nil {
		return err
	}
	after, err := eval.ReadJSON(rest[1])
	if err != nil {
		return err
	}
	return eval.WriteComparisonReport(*outPath, before, after)
}

func runGenFixture(args []string) error {
	fs := flag.NewFlagSet("gen-fixture", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	configPath := fs.String("config", "", "optional KubeSage config path")
	kubeconfig := fs.String("kubeconfig", "", "kubeconfig path override")
	namespace := fs.String("namespace", "", "pod namespace")
	podName := fs.String("pod", "", "pod name")
	containerName := fs.String("container", "", "optional container name")
	expectedFault := fs.String("expected-fault-type", "", "optional expected fault type metadata")
	outPath := fs.String("out", "", "fixture JSON output path")
	includeLogs := fs.Bool("include-logs", true, "collect current/previous logs")
	includeEvents := fs.Bool("include-events", true, "collect events")
	includeMetrics := fs.Bool("include-metrics", false, "collect metric trends when Prometheus is configured")
	timeout := fs.Duration("timeout", 30*time.Second, "collection timeout")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *namespace == "" || *podName == "" || *outPath == "" {
		return errors.New("gen-fixture requires -namespace, -pod, and -out")
	}

	cfg, err := evalConfig(*configPath, *kubeconfig, *timeout)
	if err != nil {
		return err
	}
	client, err := k8s.NewClient(cfg.Kubernetes)
	if err != nil {
		return err
	}
	snapshot := service.NewSnapshotService(cfg, client, zap.NewNop(), nil)
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	diagCtx, err := snapshot.Collect(ctx, service.PodDiagnosisRequest{
		Namespace:      *namespace,
		PodName:        *podName,
		ContainerName:  *containerName,
		ExpectedFault:  *expectedFault,
		IncludeLogs:    *includeLogs,
		IncludeEvents:  *includeEvents,
		IncludeMetrics: *includeMetrics,
	})
	if err != nil {
		return err
	}
	return eval.WriteFixture(*outPath, diagCtx)
}

func evalConfig(configPath, kubeconfig string, timeout time.Duration) (*config.Config, error) {
	cfg := &config.Config{
		Kubernetes: config.KubernetesConfig{
			Kubeconfig:            kubeconfig,
			RequestTimeoutSeconds: int(timeout.Seconds()),
			DefaultLogTailLines:   200,
		},
		Diagnosis: config.DiagnosisConfig{
			LogWindowBeforeSeconds: 120,
			LogWindowAfterSeconds:  60,
			RetryMaxAttempts:       1,
			RetryInitialBackoffMS:  0,
			RetryMaxBackoffMS:      0,
		},
	}
	if configPath == "" {
		return cfg, nil
	}
	loaded, err := config.Load(configPath)
	if err != nil {
		return nil, err
	}
	if kubeconfig != "" {
		loaded.Kubernetes.Kubeconfig = kubeconfig
	}
	if loaded.Kubernetes.RequestTimeoutSeconds <= 0 {
		loaded.Kubernetes.RequestTimeoutSeconds = int(timeout.Seconds())
	}
	if loaded.Kubernetes.DefaultLogTailLines <= 0 {
		loaded.Kubernetes.DefaultLogTailLines = 200
	}
	return loaded, nil
}

func enforceQualityGates(result *eval.EvalSuiteResult) error {
	if result == nil {
		return errors.New("no eval result")
	}
	if result.Metrics.RCAAccuracy < 1 {
		return fmt.Errorf("quality gate failed: RCA accuracy %.1f%% < 100%%", result.Metrics.RCAAccuracy*100)
	}
	if result.Metrics.HallucinationRate > 0 {
		return fmt.Errorf("quality gate failed: hallucination rate %.1f%% > 0%%", result.Metrics.HallucinationRate*100)
	}
	if result.Metrics.OverconfidenceRate > 0 {
		return fmt.Errorf("quality gate failed: overconfidence rate %.1f%% > 0%%", result.Metrics.OverconfidenceRate*100)
	}
	if result.Metrics.DangerousSuggestionCount > 0 {
		return fmt.Errorf("quality gate failed: dangerous suggestion count %d > 0", result.Metrics.DangerousSuggestionCount)
	}
	for _, r := range result.Results {
		if !r.DurationMatched {
			return fmt.Errorf("quality gate failed: case %s exceeded duration threshold (%dms > %dms)", r.ID, r.DurationMS, r.MaxDurationMS)
		}
	}
	return nil
}

func printUsage(out *os.File) {
	fmt.Fprintln(out, "Usage:")
	fmt.Fprintln(out, "  rca-eval run [--cases eval/cases] [--out eval-results.json] [--summary eval-summary.md]")
	fmt.Fprintln(out, "  rca-eval compare [--out eval-comparison.md] before.json after.json")
	fmt.Fprintln(out, "  rca-eval gen-fixture --namespace NS --pod POD --out eval/fixtures/case.json [--kubeconfig PATH]")
}
