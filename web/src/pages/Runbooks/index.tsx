import { useState } from 'react';
import { Card, Table, Button, Modal, Form, Input, Select, Space, Tag, message } from 'antd';
import { PlusOutlined, EditOutlined, FileTextOutlined } from '@ant-design/icons';
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import { listRunbooks, createRunbook, updateRunbook } from '../../api/runbooks';
import type { Runbook, RunbookRequest } from '../../api/runbooks';
import { canAccessRole } from '../../api/auth';
import { useCurrentUser } from '../../hooks/useCurrentUser';

const faultTypes = [
  'OOMKilled',
  'CrashLoopBackOff',
  'ImagePullBackOff',
  'Init:Error',
  'Evicted',
  'PodPending',
  'ProbeFailed',
  'NodeNotReady',
];

const RunbooksPage = () => {
  const queryClient = useQueryClient();
  const [modalOpen, setModalOpen] = useState(false);
  const [editingRunbook, setEditingRunbook] = useState<Runbook | null>(null);
  const [form] = Form.useForm();
  const { data: currentUser } = useCurrentUser();
  const canEditRunbooks = canAccessRole(currentUser?.role, 'admin');

  const { data: runbooks, isLoading } = useQuery({
    queryKey: ['runbooks'],
    queryFn: listRunbooks,
  });

  const createMutation = useMutation({
    mutationFn: (data: RunbookRequest) => createRunbook(data),
    onSuccess: () => {
      message.success('诊断手册已创建');
      queryClient.invalidateQueries({ queryKey: ['runbooks'] });
      setModalOpen(false);
      form.resetFields();
    },
    onError: (err: Error) => message.error(err.message),
  });

  const updateMutation = useMutation({
    mutationFn: ({ id, data }: { id: number; data: RunbookRequest }) => updateRunbook(id, data),
    onSuccess: () => {
      message.success('诊断手册已更新');
      queryClient.invalidateQueries({ queryKey: ['runbooks'] });
      setModalOpen(false);
      setEditingRunbook(null);
      form.resetFields();
    },
    onError: (err: Error) => message.error(err.message),
  });

  const handleCreate = () => {
    setEditingRunbook(null);
    form.resetFields();
    setModalOpen(true);
  };

  const handleEdit = (runbook: Runbook) => {
    setEditingRunbook(runbook);
    form.setFieldsValue({
      fault_type: runbook.fault_type,
      title: runbook.title,
      content: runbook.content,
    });
    setModalOpen(true);
  };

  const handleSubmit = () => {
    form.validateFields().then((values) => {
      const data: RunbookRequest = {
        fault_type: values.fault_type,
        title: values.title,
        content: values.content,
      };
      if (editingRunbook) {
        updateMutation.mutate({ id: editingRunbook.id, data });
      } else {
        createMutation.mutate(data);
      }
    });
  };

  const columns = [
    {
      title: 'ID',
      dataIndex: 'id',
      key: 'id',
      width: 60,
    },
    {
      title: '故障类型',
      dataIndex: 'fault_type',
      key: 'fault_type',
      render: (ft: string) => <Tag color="blue">{ft}</Tag>,
    },
    {
      title: '标题',
      dataIndex: 'title',
      key: 'title',
      ellipsis: true,
    },
    {
      title: '版本',
      dataIndex: 'version',
      key: 'version',
      width: 80,
    },
    {
      title: '创建人',
      dataIndex: 'created_by',
      key: 'created_by',
      width: 120,
    },
    {
      title: '更新时间',
      dataIndex: 'updated_at',
      key: 'updated_at',
      width: 180,
      render: (t: string) => t ? new Date(t).toLocaleString('zh-CN') : '-',
    },
    {
      title: '操作',
      key: 'action',
      width: 100,
      render: (_: unknown, record: Runbook) =>
        canEditRunbooks ? (
          <Button type="link" icon={<EditOutlined />} onClick={() => handleEdit(record)}>
            编辑
          </Button>
        ) : null,
    },
  ];

  return (
    <div>
      <Card
        title={
          <Space>
            <FileTextOutlined />
            <span>诊断手册管理</span>
          </Space>
        }
        extra={
          canEditRunbooks ? (
            <Button type="primary" icon={<PlusOutlined />} onClick={handleCreate}>
              新建手册
            </Button>
          ) : null
        }
      >
        <Table
          columns={columns}
          dataSource={runbooks || []}
          rowKey="id"
          loading={isLoading}
          pagination={{ pageSize: 20 }}
        />
      </Card>

      <Modal
        title={editingRunbook ? '编辑诊断手册' : '新建诊断手册'}
        open={modalOpen}
        onCancel={() => {
          setModalOpen(false);
          setEditingRunbook(null);
        }}
        onOk={handleSubmit}
        confirmLoading={createMutation.isPending || updateMutation.isPending}
        width={720}
      >
        <Form form={form} layout="vertical" style={{ marginTop: 16 }}>
          <Form.Item
            label="故障类型"
            name="fault_type"
            rules={[{ required: true, message: '请选择故障类型' }]}
          >
            <Select placeholder="请选择故障类型">
              {faultTypes.map((ft) => (
                <Select.Option key={ft} value={ft}>
                  {ft}
                </Select.Option>
              ))}
            </Select>
          </Form.Item>
          <Form.Item
            label="标题"
            name="title"
            rules={[{ required: true, message: '请输入标题' }]}
          >
            <Input placeholder="诊断手册标题" />
          </Form.Item>
          <Form.Item
            label="内容（Markdown）"
            name="content"
            rules={[{ required: true, message: '请输入内容' }]}
          >
            <Input.TextArea rows={12} placeholder="# 请输入 Markdown 格式的诊断步骤..." />
          </Form.Item>
        </Form>
      </Modal>
    </div>
  );
};

export default RunbooksPage;
