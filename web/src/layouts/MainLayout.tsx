import { useState, useEffect } from 'react';
import { Outlet, useNavigate, useLocation } from 'react-router-dom';
import { Layout, Menu, Button, Modal, Input, Space, Typography, Badge, Tooltip } from 'antd';
import {
  DashboardOutlined,
  UnorderedListOutlined,
  MedicineBoxOutlined,
  KeyOutlined,
  BugOutlined,
  LockOutlined,
  CheckCircleOutlined,
  FileTextOutlined,
  FileProtectOutlined,
  SafetyOutlined,
} from '@ant-design/icons';
import { setAuthModalHandler } from '../api/client';

const { Header, Sider, Content } = Layout;

const menuItems = [
  { key: '/', icon: <DashboardOutlined />, label: '仪表盘' },
  { key: '/tasks', icon: <UnorderedListOutlined />, label: '任务列表' },
  { key: '/diagnose', icon: <MedicineBoxOutlined />, label: '新建诊断' },
  { key: '/runbooks', icon: <FileTextOutlined />, label: 'Runbook 管理' },
  { key: '/approvals', icon: <SafetyOutlined />, label: '审批中心' },
  { key: '/audit-logs', icon: <FileProtectOutlined />, label: '审计日志' },
];

const MainLayout = () => {
  const navigate = useNavigate();
  const location = useLocation();
  const [collapsed, setCollapsed] = useState(false);
  const [tokenModalOpen, setTokenModalOpen] = useState(false);
  const [tokenInput, setTokenInput] = useState('');

  useEffect(() => {
    setAuthModalHandler(() => setTokenModalOpen(true));
  }, []);

  const handleSaveToken = () => {
    if (tokenInput.trim()) {
      localStorage.setItem('kubesage_token', tokenInput.trim());
      setTokenModalOpen(false);
      window.location.reload();
    }
  };

  const currentToken = localStorage.getItem('kubesage_token') || '';

  const selectedKey = location.pathname.startsWith('/tasks/')
    ? '/tasks'
    : location.pathname;

  const pageTitle = menuItems.find((m) => m.key === selectedKey)?.label || 'KubeSage';

  return (
    <Layout style={{ minHeight: '100vh' }}>
      <Sider
        collapsible
        collapsed={collapsed}
        onCollapse={setCollapsed}
        theme="light"
        width={220}
        style={{
          overflow: 'auto',
          height: '100vh',
          position: 'fixed',
          left: 0,
          top: 0,
          bottom: 0,
          zIndex: 10,
        }}
      >
        <div className="sidebar-logo" onClick={() => navigate('/')}>
          <BugOutlined className="logo-icon" />
          {!collapsed && <span className="logo-text">KubeSage</span>}
        </div>
        <Menu
          mode="inline"
          selectedKeys={[selectedKey]}
          items={menuItems}
          onClick={({ key }) => navigate(key)}
          style={{ borderRight: 'none', marginTop: 8 }}
        />
        {!collapsed && (
          <div
            style={{
              position: 'absolute',
              bottom: 60,
              left: 0,
              right: 0,
              padding: '16px 24px',
              textAlign: 'center',
            }}
          >
            <Typography.Text
              type="secondary"
              style={{ fontSize: 11, lineHeight: 1.6 }}
            >
              K8s Pod 智能诊断平台
            </Typography.Text>
          </div>
        )}
      </Sider>
      <Layout style={{ marginLeft: collapsed ? 80 : 220, transition: 'margin-left 0.2s' }}>
        <Header className="app-header">
          <span className="page-title">{pageTitle}</span>
          <Space size="middle">
            {currentToken ? (
              <Tooltip title="Token 已配置，点击修改">
                <Badge status="success" text={
                  <Typography.Text style={{ fontSize: 13 }}>
                    <CheckCircleOutlined style={{ color: '#52c41a', marginRight: 4 }} />
                    已认证
                  </Typography.Text>
                } />
              </Tooltip>
            ) : (
              <Tooltip title="未配置 Token，点击配置">
                <Badge status="warning" text={
                  <Typography.Text type="warning" style={{ fontSize: 13 }}>
                    <LockOutlined style={{ marginRight: 4 }} />
                    未认证
                  </Typography.Text>
                } />
              </Tooltip>
            )}
            <Button
              icon={<KeyOutlined />}
              onClick={() => {
                setTokenInput(currentToken);
                setTokenModalOpen(true);
              }}
              size="small"
            >
              设置 Token
            </Button>
          </Space>
        </Header>
        <Content
          style={{
            margin: 24,
            overflow: 'auto',
            minHeight: 'calc(100vh - 64px - 48px)',
          }}
        >
          <div className="page-enter">
            <Outlet />
          </div>
        </Content>
      </Layout>

      <Modal
        title={
          <Space>
            <KeyOutlined />
            配置认证 Token
          </Space>
        }
        open={tokenModalOpen}
        onOk={handleSaveToken}
        onCancel={() => setTokenModalOpen(false)}
        okText="保存"
        cancelText="取消"
        centered
      >
        <Space direction="vertical" style={{ width: '100%' }} size="middle">
          <Typography.Text type="secondary">
            输入后端 API 的 Bearer Token。Token 存储在浏览器本地，不会发送到第三方。
          </Typography.Text>
          <Input.Password
            placeholder="请输入 Token"
            value={tokenInput}
            onChange={(e) => setTokenInput(e.target.value)}
            onPressEnter={handleSaveToken}
            size="large"
            prefix={<LockOutlined style={{ color: '#bfbfbf' }} />}
          />
        </Space>
      </Modal>
    </Layout>
  );
};

export default MainLayout;
