import { Routes, Route } from 'react-router-dom';
import MainLayout from './layouts/MainLayout';
import Dashboard from './pages/Dashboard';
import TaskList from './pages/TaskList';
import TaskDetail from './pages/TaskDetail';
import Diagnose from './pages/Diagnose';
import Runbooks from './pages/Runbooks';
import AuditLogs from './pages/AuditLogs';
import Approvals from './pages/Approvals';

const App = () => (
  <Routes>
    <Route element={<MainLayout />}>
      <Route path="/" element={<Dashboard />} />
      <Route path="/tasks" element={<TaskList />} />
      <Route path="/tasks/:id" element={<TaskDetail />} />
      <Route path="/diagnose" element={<Diagnose />} />
      <Route path="/runbooks" element={<Runbooks />} />
      <Route path="/audit-logs" element={<AuditLogs />} />
      <Route path="/approvals" element={<Approvals />} />
    </Route>
  </Routes>
);

export default App;
