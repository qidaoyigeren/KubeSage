package service

import (
	"errors"
	"fmt"
	"regexp"
)

var (
	ErrInvalidDiagnosisRequest = errors.New("invalid diagnosis request")
	ErrDiagnosisAlreadyRunning = errors.New("diagnosis already running for pod")
	dnsLabelPattern            = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`)
	dnsSubdomainPattern        = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?(\.[a-z0-9]([-a-z0-9]*[a-z0-9])?)*$`)
)

// ValidatePodDiagnosisRequest checks user-controlled Kubernetes identifiers
// before they are passed into the Kubernetes API.
func ValidatePodDiagnosisRequest(req PodDiagnosisRequest) error {
	if !validDNSLabel(req.Namespace, 63) {
		return fmt.Errorf("%w: namespace must be a valid RFC1123 label", ErrInvalidDiagnosisRequest)
	}
	if !validDNSSubdomain(req.PodName, 253) {
		return fmt.Errorf("%w: pod_name must be a valid RFC1123 subdomain", ErrInvalidDiagnosisRequest)
	}
	if req.ContainerName != "" && !validDNSLabel(req.ContainerName, 63) {
		return fmt.Errorf("%w: container_name must be a valid RFC1123 label", ErrInvalidDiagnosisRequest)
	}
	return nil
}

// validDNSLabel validates Kubernetes namespace/container-style names.
func validDNSLabel(value string, maxLen int) bool {
	if value == "" || len(value) > maxLen {
		return false
	}
	return dnsLabelPattern.MatchString(value)
}

// validDNSSubdomain validates Kubernetes pod-style names.
func validDNSSubdomain(value string, maxLen int) bool {
	if value == "" || len(value) > maxLen {
		return false
	}
	return dnsSubdomainPattern.MatchString(value)
}
