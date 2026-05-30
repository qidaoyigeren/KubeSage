import { Card, Form, Input, Select, Checkbox, Button, Space, Typography, App } from 'antd';
import {
  MedicineBoxOutlined,
  InfoCircleOutlined,
} from '@ant-design/icons';
import { useNavigate } from 'react-router-dom';
import { useQueryClient } from '@tanstack/react-query';
import { useDiagnose } from '../../hooks/useDiagnose';

const Diagnose = () => {
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const { message } = App.useApp();
  const diagnose = useDiagnose();
  const [form] = Form.useForm();

  const handleSubmit = async (values: Record<string, unknown>) => {
    try {
      const task = await diagnose.mutateAsync({
        namespace: values.namespace as string,
        pod_name: values.pod_name as string,
        container_name: (values.container_name as string) || undefined,
        expected_fault_type: (values.expected_fault_type as string) || undefined,
        alert_name: (values.alert_name as string) || undefined,
        alert_severity: (values.alert_severity as string) || undefined,
        include_logs: values.include_logs !== false,
        include_events: values.include_events !== false,
        include_metrics: values.include_metrics !== false,
      });
      queryClient.invalidateQueries({ queryKey: ['tasks'] });
      message.success('诊断任务已创建，正在跳转...');
      navigate(`/tasks/${task.id}`);
    } catch (err: unknown) {
      const msg = err instanceof Error ? err.message : '提交失败';
      message.error(msg);
    }
  };

  return (
    <div style={{ maxWidth: 720 }}>
      <Card
        className="diagnose-form-card"
        bordered={false}
      >
        <div style={{ padding: '0 0 24px' }}>
          <Form
            form={form}
            layout="vertical"
            onFinish={handleSubmit}
            initialValues={{
              include_logs: true,
              include_events: true,
              include_metrics: true,
            }}
            size="large"
          >
            <Typography.Title level={5} style={{ marginBottom: 16 }}>
              <MedicineBoxOutlined style={{ marginRight: 8, color: '#667eea' }} />
              基本信息
            </Typography.Title>

            <Form.Item
              label="Namespace"
              name="namespace"
              rules={[{ required: true, message: '请输入 Namespace' }]}
              extra="Kubernetes 命名空间名称"
            >
              <Input placeholder="例如: default, production" />
            </Form.Item>

            <Form.Item
              label="Pod 名称"
              name="pod_name"
              rules={[{ required: true, message: '请输入 Pod 名称' }]}
              extra="完整的 Pod 名称，包含随机后缀"
            >
              <Input placeholder="例如: my-app-7d8b9c-xk2z4" />
            </Form.Item>

            <Form.Item
              label="容器名称"
              name="container_name"
              extra="多容器 Pod 时指定目标容器"
            >
              <Input placeholder="可选，留空则自动检测" />
            </Form.Item>

            <Typography.Title level={5} style={{ marginBottom: 16, marginTop: 8 }}>
              <InfoCircleOutlined style={{ marginRight: 8, color: '#667eea' }} />
              诊断参数（可选）
            </Typography.Title>

            <Form.Item label="预期故障类型" name="expected_fault_type">
              <Select
                allowClear
                placeholder="选择故障类型可提高诊断精度"
                options={[
                  { value: 'CrashLoopBackOff', label: 'CrashLoopBackOff — 容器反复崩溃重启' },
                  { value: 'OOMKilled', label: 'OOMKilled — 内存溢出被杀' },
                  { value: 'Pending', label: 'Pending — Pod 无法调度' },
                  { value: 'ProbeFailed', label: 'ProbeFailed — 健康检查失败' },
                ]}
              />
            </Form.Item>

            <Form.Item label="告警名称" name="alert_name">
              <Input placeholder="例如: KubePodCrashLooping" />
            </Form.Item>

            <Form.Item label="告警级别" name="alert_severity">
              <Select
                allowClear
                placeholder="选择告警级别"
                options={[
                  { value: 'critical', label: 'Critical — 严重' },
                  { value: 'warning', label: 'Warning — 警告' },
                  { value: 'info', label: 'Info — 信息' },
                ]}
              />
            </Form.Item>

            <Typography.Title level={5} style={{ marginBottom: 16, marginTop: 8 }}>
              采集选项
            </Typography.Title>

            <Form.Item>
              <Space size="large">
                <Form.Item name="include_logs" valuePropName="checked" noStyle>
                  <Checkbox>采集日志</Checkbox>
                </Form.Item>
                <Form.Item name="include_events" valuePropName="checked" noStyle>
                  <Checkbox>采集事件</Checkbox>
                </Form.Item>
                <Form.Item name="include_metrics" valuePropName="checked" noStyle>
                  <Checkbox>采集指标</Checkbox>
                </Form.Item>
              </Space>
            </Form.Item>

            <Form.Item style={{ marginTop: 32 }}>
              <Space size="middle">
                <Button
                  type="primary"
                  htmlType="submit"
                  loading={diagnose.isPending}
                  size="large"
                  style={{
                    background: 'linear-gradient(135deg, #667eea 0%, #764ba2 100%)',
                    border: 'none',
                    minWidth: 160,
                    height: 44,
                    fontWeight: 600,
                  }}
                >
                  {diagnose.isPending ? '正在提交...' : '开始诊断'}
                </Button>
                <Button
                  onClick={() => form.resetFields()}
                  size="large"
                  style={{ height: 44 }}
                >
                  重置表单
                </Button>
              </Space>
            </Form.Item>
          </Form>
        </div>
      </Card>
    </div>
  );
};

export default Diagnose;
