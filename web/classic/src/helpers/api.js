/*
Copyright (C) 2025 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/

import {
  getUserIdFromLocalStorage,
  showError,
  formatMessageForAPI,
  isValidMessage,
} from './utils';
import axios from 'axios';
import { MESSAGE_ROLES } from '../constants/playground.constants';

const dashboardServerBaseURL =
  import.meta.env.VITE_REACT_APP_SERVER_URL || '';

export let API = axios.create({
  baseURL: dashboardServerBaseURL,
  headers: {
    'New-API-User': getUserIdFromLocalStorage(),
    'Cache-Control': 'no-store',
  },
  withCredentials: true,
});

let dashboardAccessToken = null;
let dashboardAccessTokenExpiresAt = 0;
let dashboardRefreshPromise = null;
const dashboardRefreshLockName = 'new-api:dashboard-auth-refresh';
const dashboardRefreshLeaseKey = `${dashboardRefreshLockName}:lease`;
const dashboardRefreshChannel =
  typeof BroadcastChannel === 'undefined'
    ? null
    : new BroadcastChannel(dashboardRefreshLockName);

function setDashboardAccessToken(token, broadcast = false, expiresAt = 0) {
  dashboardAccessToken = token;
  dashboardAccessTokenExpiresAt = token ? expiresAt : 0;
  if (broadcast) {
    dashboardRefreshChannel?.postMessage({
      type: token ? 'token' : 'logout',
      token,
      expiresAt: dashboardAccessTokenExpiresAt,
    });
  }
}

dashboardRefreshChannel?.addEventListener('message', (event) => {
  if (event.data?.type === 'token' && typeof event.data.token === 'string') {
    setDashboardAccessToken(
      event.data.token,
      false,
      Number(event.data.expiresAt) || 0,
    );
  } else if (event.data?.type === 'logout') {
    setDashboardAccessToken(null);
  }
});

function delay(milliseconds) {
  return new Promise((resolve) => window.setTimeout(resolve, milliseconds));
}

function clearExpiredDashboardAccessToken() {
  if (
    dashboardAccessToken &&
    dashboardAccessTokenExpiresAt > 0 &&
    dashboardAccessTokenExpiresAt <= Math.floor(Date.now() / 1000) + 5
  ) {
    setDashboardAccessToken(null);
  }
}

async function withCrossTabRefreshLock(refresh) {
  if (navigator.locks) {
    await navigator.locks.request(dashboardRefreshLockName, async () => {
      if (!dashboardAccessToken) await refresh();
    });
    return;
  }

  const owner =
    typeof crypto.randomUUID === 'function'
      ? crypto.randomUUID()
      : `${Date.now()}-${Math.random()}`;
  for (;;) {
    if (dashboardAccessToken) return;
    const now = Date.now();
    let lease = null;
    try {
      lease = JSON.parse(
        localStorage.getItem(dashboardRefreshLeaseKey) || 'null',
      );
    } catch {
      lease = null;
    }
    if (!lease?.expiresAt || lease.expiresAt <= now) {
      localStorage.setItem(
        dashboardRefreshLeaseKey,
        JSON.stringify({ owner, expiresAt: now + 15_000 }),
      );
      await delay(25);
      const claimed = JSON.parse(
        localStorage.getItem(dashboardRefreshLeaseKey) || 'null',
      );
      if (claimed?.owner === owner) {
        try {
          if (!dashboardAccessToken) await refresh();
          return;
        } finally {
          const current = JSON.parse(
            localStorage.getItem(dashboardRefreshLeaseKey) || 'null',
          );
          if (current?.owner === owner) {
            localStorage.removeItem(dashboardRefreshLeaseKey);
          }
        }
      }
    }
    await delay(75);
  }
}

async function refreshDashboardAccessToken() {
  dashboardRefreshPromise ??= withCrossTabRefreshLock(async () => {
    const response = await axios.post('/api/user/refresh', undefined, {
      baseURL: dashboardServerBaseURL,
      withCredentials: true,
    });
    const token = response?.data?.data?.access_token;
    if (
      response?.data?.success !== true ||
      typeof token !== 'string' ||
      token === ''
    ) {
      throw new Error(response?.data?.message || 'refresh failed');
    }
    setDashboardAccessToken(
      token,
      true,
      Number(response?.data?.data?.access_expires_at) || 0,
    );
  }).finally(() => {
    dashboardRefreshPromise = null;
  });
  try {
    await dashboardRefreshPromise;
  } catch (error) {
    const status = error?.response?.status;
    if (status === 401 || status === 403) {
      setDashboardAccessToken(null, true);
      localStorage.removeItem('user');
    }
    throw error;
  }
}

export async function getDashboardAuthHeaders() {
  clearExpiredDashboardAccessToken();
  if (!dashboardAccessToken && localStorage.getItem('user') !== null) {
    await refreshDashboardAccessToken();
  }
  return dashboardAccessToken
    ? { Authorization: `Bearer ${dashboardAccessToken}` }
    : {};
}

function isDashboardAuthResponse(url = '') {
  return (
    url.includes('/api/user/login') ||
    url.includes('/api/user/register') ||
    url.includes('/api/user/passkey/login/finish') ||
    url.includes('/api/user/refresh') ||
    url.includes('/api/oauth/')
  );
}

async function attachDashboardAccessToken(config) {
  const url = config.url || '';
  const hasPersistedUser = localStorage.getItem('user') !== null;
  clearExpiredDashboardAccessToken();
  if (
    !dashboardAccessToken &&
    hasPersistedUser &&
    !url.endsWith('/api/user/refresh')
  ) {
    await refreshDashboardAccessToken();
  }
  if (dashboardAccessToken) {
    config.headers.Authorization = `Bearer ${dashboardAccessToken}`;
  }
  return config;
}

function handleDashboardAuthResponse(response) {
  if (response.config.url?.endsWith('/api/user/logout')) {
    setDashboardAccessToken(null, true);
  }
  const data = response?.data?.data;
  if (
    isDashboardAuthResponse(response.config.url) &&
    data &&
    typeof data === 'object' &&
    typeof data.access_token === 'string'
  ) {
    setDashboardAccessToken(
      data.access_token,
      true,
      Number(data.access_expires_at) || 0,
    );
    delete data.access_token;
    delete data.refresh_token;
    delete data.token;
  }
  return response;
}

async function handleDashboardAuthError(error) {
  const config = error?.config;
  const isRefreshRequest = config?.url?.endsWith('/api/user/refresh');
  if (
    error?.response?.status === 401 &&
    !isRefreshRequest &&
    !config?.dashboardAuthRetried &&
    localStorage.getItem('user') !== null
  ) {
    config.dashboardAuthRetried = true;
    setDashboardAccessToken(null);
    await refreshDashboardAccessToken();
    if (dashboardAccessToken) {
      config.headers.Authorization = `Bearer ${dashboardAccessToken}`;
    }
    return API.request(config);
  }
  return Promise.reject(error);
}

function redirectToOAuthUrl(url, options = {}) {
  const { openInNewTab = false } = options;
  const targetUrl = typeof url === 'string' ? url : url.toString();

  if (openInNewTab) {
    window.open(targetUrl, '_blank');
    return;
  }

  window.location.assign(targetUrl);
}

function patchAPIInstance(instance) {
  const originalGet = instance.get.bind(instance);
  const inFlightGetRequests = new Map();

  const genKey = (url, config = {}) => {
    const params = config.params ? JSON.stringify(config.params) : '{}';
    return `${url}?${params}`;
  };

  instance.get = (url, config = {}) => {
    if (config?.disableDuplicate) {
      return originalGet(url, config);
    }

    const key = genKey(url, config);
    if (inFlightGetRequests.has(key)) {
      return inFlightGetRequests.get(key);
    }

    const reqPromise = originalGet(url, config).finally(() => {
      inFlightGetRequests.delete(key);
    });

    inFlightGetRequests.set(key, reqPromise);
    return reqPromise;
  };
}

function configureAPIInstance(instance) {
  instance.interceptors.request.use(attachDashboardAccessToken);
  instance.interceptors.response.use(
    handleDashboardAuthResponse,
    handleDashboardAuthError,
  );
  instance.interceptors.response.use(
    (response) => response,
    (error) => {
      // 如果请求配置中显式要求跳过全局错误处理，则不弹出默认错误提示
      if (error.config && error.config.skipErrorHandler) {
        return Promise.reject(error);
      }
      showError(error);
      return Promise.reject(error);
    },
  );
  patchAPIInstance(instance);
}

configureAPIInstance(API);

export function updateAPI() {
  API = axios.create({
    baseURL: dashboardServerBaseURL,
    headers: {
      'New-API-User': getUserIdFromLocalStorage(),
      'Cache-Control': 'no-store',
    },
    withCredentials: true,
  });

  configureAPIInstance(API);
}

// playground

// 构建API请求负载
export const buildApiPayload = (
  messages,
  systemPrompt,
  inputs,
  parameterEnabled,
) => {
  const processedMessages = messages
    .filter(isValidMessage)
    .map(formatMessageForAPI)
    .filter(Boolean);

  // 如果有系统提示，插入到消息开头
  if (systemPrompt && systemPrompt.trim()) {
    processedMessages.unshift({
      role: MESSAGE_ROLES.SYSTEM,
      content: systemPrompt.trim(),
    });
  }

  const payload = {
    model: inputs.model,
    group: inputs.group,
    messages: processedMessages,
    stream: inputs.stream,
  };

  // 添加启用的参数
  const parameterMappings = {
    temperature: 'temperature',
    top_p: 'top_p',
    max_tokens: 'max_tokens',
    frequency_penalty: 'frequency_penalty',
    presence_penalty: 'presence_penalty',
    seed: 'seed',
  };

  Object.entries(parameterMappings).forEach(([key, param]) => {
    const enabled = parameterEnabled[key];
    const value = inputs[param];
    const hasValue = value !== undefined && value !== null;

    if (!enabled) {
      return;
    }

    if (param === 'max_tokens') {
      if (typeof value === 'number') {
        payload[param] = value;
      }
      return;
    }

    if (hasValue) {
      payload[param] = value;
    }
  });

  return payload;
};

// 处理API错误响应
export const handleApiError = (error, response = null) => {
  const errorInfo = {
    error: error.message || '未知错误',
    timestamp: new Date().toISOString(),
    stack: error.stack,
  };

  if (response) {
    errorInfo.status = response.status;
    errorInfo.statusText = response.statusText;
  }

  if (error.message.includes('HTTP error')) {
    errorInfo.details = '服务器返回了错误状态码';
  } else if (error.message.includes('Failed to fetch')) {
    errorInfo.details = '网络连接失败或服务器无响应';
  }

  return errorInfo;
};

// 处理模型数据
export const processModelsData = (data, currentModel) => {
  const modelOptions = data.map((model) => ({
    label: model,
    value: model,
  }));

  const hasCurrentModel = modelOptions.some(
    (option) => option.value === currentModel,
  );
  const selectedModel =
    hasCurrentModel && modelOptions.length > 0
      ? currentModel
      : modelOptions[0]?.value;

  return { modelOptions, selectedModel };
};

// 处理分组数据
export const processGroupsData = (data, userGroup) => {
  let groupOptions = Object.entries(data).map(([group, info]) => ({
    label:
      info.desc.length > 20 ? info.desc.substring(0, 20) + '...' : info.desc,
    value: group,
    ratio: info.ratio,
    fullLabel: info.desc,
  }));

  if (groupOptions.length === 0) {
    groupOptions = [
      {
        label: '用户分组',
        value: '',
        ratio: 1,
      },
    ];
  } else if (userGroup) {
    const userGroupIndex = groupOptions.findIndex((g) => g.value === userGroup);
    if (userGroupIndex > -1) {
      const userGroupOption = groupOptions.splice(userGroupIndex, 1)[0];
      groupOptions.unshift(userGroupOption);
    }
  }

  return groupOptions;
};

// 原来components中的utils.js

export async function getOAuthState() {
  let path = '/api/oauth/state';
  let affCode = localStorage.getItem('aff');
  if (affCode && affCode.length > 0) {
    path += `?aff=${affCode}`;
  }
  const res = await API.get(path);
  const { success, message, data } = res.data;
  if (success) {
    return data;
  } else {
    showError(message);
    return '';
  }
}

async function prepareOAuthState(options = {}) {
  const { shouldLogout = false } = options;
  if (shouldLogout) {
    try {
      await API.post('/api/user/logout', undefined, { skipErrorHandler: true });
    } catch (err) {}
    localStorage.removeItem('user');
    updateAPI();
  }
  return await getOAuthState();
}

export async function onDiscordOAuthClicked(client_id, options = {}) {
  const state = await prepareOAuthState(options);
  if (!state) return;
  const redirect_uri = `${window.location.origin}/oauth/discord`;
  const response_type = 'code';
  const scope = 'identify+openid';
  redirectToOAuthUrl(
    `https://discord.com/oauth2/authorize?client_id=${client_id}&redirect_uri=${redirect_uri}&response_type=${response_type}&scope=${scope}&state=${state}`,
  );
}

export async function onOIDCClicked(
  auth_url,
  client_id,
  openInNewTab = false,
  options = {},
) {
  const state = await prepareOAuthState(options);
  if (!state) return;
  const url = new URL(auth_url);
  url.searchParams.set('client_id', client_id);
  url.searchParams.set('redirect_uri', `${window.location.origin}/oauth/oidc`);
  url.searchParams.set('response_type', 'code');
  url.searchParams.set('scope', 'openid profile email');
  url.searchParams.set('state', state);
  redirectToOAuthUrl(url, { openInNewTab });
}

export async function onGitHubOAuthClicked(github_client_id, options = {}) {
  const state = await prepareOAuthState(options);
  if (!state) return;
  redirectToOAuthUrl(
    `https://github.com/login/oauth/authorize?client_id=${github_client_id}&state=${state}&scope=user:email`,
  );
}

export async function onLinuxDOOAuthClicked(
  linuxdo_client_id,
  options = { shouldLogout: false },
) {
  const state = await prepareOAuthState(options);
  if (!state) return;
  redirectToOAuthUrl(
    `https://connect.linux.do/oauth2/authorize?response_type=code&client_id=${linuxdo_client_id}&state=${state}`,
  );
}

/**
 * Initiate custom OAuth login
 * @param {Object} provider - Custom OAuth provider config from status API
 * @param {string} provider.slug - Provider slug (used for callback URL)
 * @param {string} provider.client_id - OAuth client ID
 * @param {string} provider.authorization_endpoint - Authorization URL
 * @param {string} provider.scopes - OAuth scopes (space-separated)
 * @param {Object} options - Options
 * @param {boolean} options.shouldLogout - Whether to logout first
 */
export async function onCustomOAuthClicked(provider, options = {}) {
  const state = await prepareOAuthState(options);
  if (!state) return;

  try {
    const redirect_uri = `${window.location.origin}/oauth/${provider.slug}`;

    // Check if authorization_endpoint is a full URL or relative path
    let authUrl;
    if (
      provider.authorization_endpoint.startsWith('http://') ||
      provider.authorization_endpoint.startsWith('https://')
    ) {
      authUrl = new URL(provider.authorization_endpoint);
    } else {
      // Relative path - this is a configuration error, show error message
      console.error(
        'Custom OAuth authorization_endpoint must be a full URL:',
        provider.authorization_endpoint,
      );
      showError(
        'OAuth 配置错误：授权端点必须是完整的 URL（以 http:// 或 https:// 开头）',
      );
      return;
    }

    authUrl.searchParams.set('client_id', provider.client_id);
    authUrl.searchParams.set('redirect_uri', redirect_uri);
    authUrl.searchParams.set('response_type', 'code');
    authUrl.searchParams.set(
      'scope',
      provider.scopes || 'openid profile email',
    );
    authUrl.searchParams.set('state', state);

    redirectToOAuthUrl(authUrl);
  } catch (error) {
    console.error('Failed to initiate custom OAuth:', error);
    showError('OAuth 登录失败：' + (error.message || '未知错误'));
  }
}

let channelModels = undefined;
export async function loadChannelModels() {
  const res = await API.get('/api/models');
  const { success, data } = res.data;
  if (!success) {
    return;
  }
  channelModels = data;
  localStorage.setItem('channel_models', JSON.stringify(data));
}

export function getChannelModels(type) {
  if (channelModels !== undefined && type in channelModels) {
    if (!channelModels[type]) {
      return [];
    }
    return channelModels[type];
  }
  let models = localStorage.getItem('channel_models');
  if (!models) {
    return [];
  }
  channelModels = JSON.parse(models);
  if (type in channelModels) {
    return channelModels[type];
  }
  return [];
}
