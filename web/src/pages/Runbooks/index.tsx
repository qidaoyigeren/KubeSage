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
      message.success('Runbook created');
      queryClient.invalidateQueries({ queryKey: ['runbooks'] });
      setModalOpen(false);
      form.resetFields();
    },
    onError: (err: Error) => message.error(err.message),
  });

  const updateMutation = useMutation({
    mutationFn: ({ id, data }: { id: number; data: RunbookRequest }) => updateRunbook(id, data),
    onSuccess: () => {
      message.success('Runbook updated');
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
      title: 'Fault Type',
      dataIndex: 'fault_type',
      key: 'fault_type',
      render: (ft: string) => <Tag color="blue">{ft}</Tag>,
    },
    {
      title: 'Title',
      dataIndex: 'title',
      key: 'title',
      ellipsis: true,
    },
    {
      title: 'Version',
      dataIndex: 'version',
      key: 'version',
      width: 80,
    },
    {
      title: 'Created By',
      dataIndex: 'created_by',
      key: 'created_by',
      width: 120,
    },
    {
      title: 'Updated',
      dataIndex: 'updated_at',
      key: 'updated_at',
      width: 180,
      render: (t: string) => t ? new Date(t).toLocaleString('zh-CN') : '-',
    },
    {
      title: 'Action',
      key: 'action',
      width: 100,
      render: (_: unknown, record: Runbook) =>
        canEditRunbooks ? (
          <Button type="link" icon={<EditOutlined />} onClick={() => handleEdit(record)}>
            Edit
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
            <span>Runbook Management</span>
          </Space>
        }
        extra={
          canEditRunbooks ? (
            <Button type="primary" icon={<PlusOutlined />} onClick={handleCreate}>
              New Runbook
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
        title={editingRunbook ? 'Edit Runbook' : 'Create Runbook'}
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
            label="Fault Type"
            name="fault_type"
            rules={[{ required: true, message: 'Please select fault type' }]}
          >
            <Select placeholder="Select fault type">
              {faultTypes.map((ft) => (
                <Select.Option key={ft} value={ft}>
                  {ft}
                </Select.Option>
              ))}
            </Select>
          </Form.Item>
          <Form.Item
            label="Title"
            name="title"
            rules={[{ required: true, message: 'Please enter title' }]}
          >
            <Input placeholder="Runbook title" />
          </Form.Item>
          <Form.Item
            label="Content (Markdown)"
            name="content"
            rules={[{ required: true, message: 'Please enter content' }]}
          >
            <Input.TextArea rows={12} placeholder="# Runbook content in markdown format..." />
          </Form.Item>
        </Form>
      </Modal>
    </div>
  );
};

export default RunbooksPage;
