import axios from 'axios';

const client = axios.create({
  baseURL: '/api/v1',
  timeout: 30000,
});

client.interceptors.request.use((config) => {
  const token = localStorage.getItem('kubesage_token');
  if (token) {
    config.headers.Authorization = `Bearer ${token}`;
  }
  return config;
});

let showAuthModal: (() => void) | null = null;

export const setAuthModalHandler = (handler: () => void) => {
  showAuthModal = handler;
};

client.interceptors.response.use(
  (response) => {
    const { code, message, data } = response.data;
    if (code !== 0) {
      return Promise.reject(new Error(message || 'request failed'));
    }
    return data;
  },
  (error) => {
    if (error.response?.status === 401) {
      showAuthModal?.();
    }
    const msg = error.response?.data?.message || error.message || 'network error';
    return Promise.reject(new Error(msg));
  },
);

export default client;
