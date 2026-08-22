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

import React, { useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';
import {
  Button,
  Form,
  Input,
  Modal,
  Popconfirm,
  Switch,
  Table,
  Tabs,
  TabPane,
  Tag,
  Typography,
  Space,
  Banner,
  InputNumber,
  Tooltip,
} from '@douyinfe/semi-ui';
import { IconTickCircle, IconAlertCircle, IconRefresh } from '@douyinfe/semi-icons';
import { API, showError, showSuccess, timestamp2string, copy } from '../../helpers';

const PAGE_SIZE = 20;
const ACTION_BLOCK = 1;
const ACTION_MONITOR = 2;

// FreezeTag 显示规则当前是否会冻结 Token：BLOCK 即冻结，MONITOR 即仅记录。
const FreezeTag = ({ action, t }) => {
  if (action === ACTION_BLOCK) {
    return <Tag color='red' size='small'>{t('记录+冻结Token')}</Tag>;
  }
  return <Tag color='blue' size='small'>{t('仅记录')}</Tag>;
};

const SensitiveMonitor = () => {
  const { t } = useTranslation();
  return (
    <div className='mt-[60px] px-2'>
      <Tabs
        type='line'
        defaultActiveKey='words'
        tabBarExtraContent={<MasterSwitchBar />}
      >
        <TabPane tab={t('关键词管理')} itemKey='words'>
          <WordsPanel />
        </TabPane>
        <TabPane tab={t('命中记录')} itemKey='blocks'>
          <BlocksPanel />
        </TabPane>
      </Tabs>
    </div>
  );
};

// ----------- 一键开关（嵌在 Tabs 右侧 extra 区） -----------
//
// 设计：与 Tabs 同行右对齐。状态用图标 + 文字色彩传达，不用整条 Banner 占行。
//   - 开：绿色勾 + "已启用"，主开关亮绿
//   - 关：橙色感叹 + "已关闭"，主开关灰
//   - 子项 (采样率 / Body 落盘) 仅在主开关 ON 时可调
const MasterSwitchBar = () => {
  const { t } = useTranslation();
  const [enabled, setEnabled] = useState(false);
  const [sampleRate, setSampleRate] = useState(20);
  const [dumpToFile, setDumpToFile] = useState(true);
  const [loading, setLoading] = useState(false);
  const [stats, setStats] = useState(null);
  const [statsLoading, setStatsLoading] = useState(false);

  const load = async () => {
    try {
      const res = await API.get('/api/option/');
      const { success, data } = res.data || {};
      if (!success || !Array.isArray(data)) return;
      const map = Object.fromEntries(data.map((o) => [o.key, o.value]));
      setEnabled(map.SensitiveAsyncEnabled === 'true');
      setDumpToFile(map.SensitiveDumpToFile !== 'false');
      const r = parseInt(map.SensitiveSampleRate, 10);
      if (!Number.isNaN(r)) setSampleRate(r);
    } catch (e) {
      // 静默：非 admin 或接口受限时不影响页面其它功能
    }
  };

  const loadStats = async () => {
    setStatsLoading(true);
    try {
      const res = await API.get('/api/sensitive_block/stats');
      if (res.data && res.data.success) {
        setStats(res.data.data);
      }
    } catch (e) {
      // 静默
    } finally {
      setStatsLoading(false);
    }
  };

  useEffect(() => {
    load();
    loadStats();
  }, []);

  const updateOption = async (key, value) => {
    try {
      setLoading(true);
      const res = await API.put('/api/option/', { key, value: String(value) });
      if (!res.data.success) {
        showError(res.data.message || t('保存失败'));
        return false;
      }
      return true;
    } catch (e) {
      showError(e.message);
      return false;
    } finally {
      setLoading(false);
    }
  };

  const toggleEnabled = async (v) => {
    const ok = await updateOption('SensitiveAsyncEnabled', v ? 'true' : 'false');
    if (ok) {
      setEnabled(v);
      showSuccess(v ? t('已启用不良监控') : t('已关闭不良监控'));
    }
  };

  const toggleDump = async (v) => {
    const ok = await updateOption('SensitiveDumpToFile', v ? 'true' : 'false');
    if (ok) setDumpToFile(v);
  };

  const saveSampleRate = async (v) => {
    const r = Math.max(0, Math.min(100, Number(v) || 0));
    const ok = await updateOption('SensitiveSampleRate', String(r));
    if (ok) {
      setSampleRate(r);
      showSuccess(t('采样率已保存'));
    }
  };

  return (
    <Space className='pr-3' spacing={12}>
      <Tooltip content={enabled ? t('不良监控已启用：异步抽查模式') : t('不良监控已关闭')}>
        <span className='inline-flex items-center'>
          {enabled ? (
            <IconTickCircle style={{ color: 'var(--semi-color-success)' }} />
          ) : (
            <IconAlertCircle style={{ color: 'var(--semi-color-warning)' }} />
          )}
          <Typography.Text className='ml-1' type={enabled ? 'success' : 'warning'}>
            {enabled ? t('已启用') : t('已关闭')}
          </Typography.Text>
        </span>
      </Tooltip>
      <Switch
        checked={enabled}
        onChange={toggleEnabled}
        loading={loading}
        checkedText={t('开')}
        uncheckedText={t('关')}
      />
      <Typography.Text type='tertiary'>{t('采样率')}</Typography.Text>
      <InputNumber
        value={sampleRate}
        min={0}
        max={100}
        step={5}
        suffix='%'
        size='small'
        style={{ width: 96 }}
        onBlur={(e) => saveSampleRate(e.target.value)}
        disabled={!enabled}
      />
      <Typography.Text type='tertiary'>{t('Body 落盘')}</Typography.Text>
      <Switch
        checked={dumpToFile}
        onChange={toggleDump}
        loading={loading}
        disabled={!enabled}
        checkedText={t('开')}
        uncheckedText={t('关')}
      />
      <Tooltip
        content={
          stats ? (
            <div className='text-xs leading-5'>
              <div className='font-medium mb-1'>{t('审计实时统计（异步抽查管道）')}</div>
              <div>{t('已入队')}: {stats.enqueued ?? 0}</div>
              <div>{t('已处理')}: {stats.processed ?? 0}</div>
              <div>{t('已丢弃')}: {stats.dropped ?? 0}</div>
              <div>{t('失败')}: {stats.failed ?? 0}</div>
              <div>{t('队列深度')}: {stats.queue_depth ?? '-'} / {stats.queue_cap ?? '-'}</div>
            </div>
          ) : t('刷新统计')
        }
      >
        <Button
          icon={<IconRefresh />}
          size='small'
          theme='borderless'
          loading={statsLoading}
          onClick={loadStats}
        >
          {stats ? `${stats.queue_depth ?? '-'}/${stats.queue_cap ?? '-'}` : t('审计队列')}
        </Button>
      </Tooltip>
    </Space>
  );
};

// ----------- 关键词管理 -----------

const WordsPanel = () => {
  const { t } = useTranslation();
  const [data, setData] = useState([]);
  const [loading, setLoading] = useState(false);
  const [page, setPage] = useState(1);
  const [total, setTotal] = useState(0);
  const [keyword, setKeyword] = useState('');
  const [modalOpen, setModalOpen] = useState(false);
  const [editing, setEditing] = useState(null);
  // 默认 ACTION_MONITOR：命中只记录、不冻结 token，符合"默认不冻结"
  const [form, setForm] = useState({ pattern: '', is_regex: false, enabled: true, action: ACTION_MONITOR, description: '' });

  const load = async (p = page, kw = keyword) => {
    setLoading(true);
    try {
      const res = await API.get(`/api/sensitive_word/?p=${p}&page_size=${PAGE_SIZE}&keyword=${encodeURIComponent(kw)}`);
      const { success, message, data } = res.data;
      if (!success) {
        showError(message);
        return;
      }
      setData(data.items || []);
      setTotal(data.total || 0);
    } catch (e) {
      showError(e.message);
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    load(1, '');
  }, []);

  const openCreate = () => {
    setEditing(null);
    setForm({ pattern: '', is_regex: false, enabled: true, action: ACTION_MONITOR, description: '' });
    setModalOpen(true);
  };

  const openEdit = (record) => {
    setEditing(record);
    setForm({
      pattern: record.pattern,
      is_regex: record.is_regex,
      enabled: record.enabled,
      action: record.action === ACTION_BLOCK ? ACTION_BLOCK : ACTION_MONITOR,
      description: record.description || '',
    });
    setModalOpen(true);
  };

  const submit = async () => {
    if (!form.pattern.trim()) {
      showError(t('关键词不能为空'));
      return;
    }
    try {
      const payload = {
        pattern: form.pattern.trim(),
        is_regex: !!form.is_regex,
        enabled: !!form.enabled,
        action: form.action === ACTION_BLOCK ? ACTION_BLOCK : ACTION_MONITOR,
        description: form.description || '',
      };
      const res = editing
        ? await API.put(`/api/sensitive_word/${editing.id}`, payload)
        : await API.post('/api/sensitive_word/', payload);
      const { success, message } = res.data;
      if (!success) {
        showError(message);
        return;
      }
      showSuccess(editing ? t('更新成功') : t('新增成功'));
      setModalOpen(false);
      load(page, keyword);
    } catch (e) {
      showError(e.message);
    }
  };

  const toggleEnabled = async (record) => {
    try {
      const res = await API.put(`/api/sensitive_word/${record.id}/toggle`);
      const { success, message } = res.data;
      if (!success) {
        showError(message);
        return;
      }
      load(page, keyword);
    } catch (e) {
      showError(e.message);
    }
  };

  const remove = async (record) => {
    try {
      const res = await API.delete(`/api/sensitive_word/${record.id}`);
      const { success, message } = res.data;
      if (!success) {
        showError(message);
        return;
      }
      showSuccess(t('已删除'));
      load(page, keyword);
    } catch (e) {
      showError(e.message);
    }
  };

  const columns = [
    { title: 'ID', dataIndex: 'id', width: 70 },
    {
      title: t('关键词'),
      dataIndex: 'pattern',
      render: (v, r) => (
        <Space>
          <Typography.Text copyable={{ content: v }}>{v}</Typography.Text>
          {r.is_regex && <Tag color='blue' size='small'>{t('正则')}</Tag>}
        </Space>
      ),
    },
    {
      title: t('命中后行为'),
      dataIndex: 'action',
      width: 140,
      render: (v) => <FreezeTag action={v} t={t} />,
    },
    {
      title: t('命中次数'),
      dataIndex: 'hit_count',
      width: 100,
      // 后端已默认按 hit_count desc 排序，前端表头点击是次级排序
      sorter: (a, b) => (a.hit_count || 0) - (b.hit_count || 0),
      render: (v, r) => {
        const n = v || 0;
        return n > 0
          ? <Tag color={r.action === ACTION_BLOCK ? 'red' : 'blue'}>{n}</Tag>
          : <Typography.Text type='tertiary'>0</Typography.Text>;
      },
    },
    {
      title: t('最近命中'),
      dataIndex: 'last_hit_at',
      width: 160,
      render: (v) => (v ? timestamp2string(v) : '-'),
    },
    { title: t('描述'), dataIndex: 'description' },
    {
      title: t('启用'),
      dataIndex: 'enabled',
      width: 90,
      render: (v, r) => (
        <Switch checked={!!v} onChange={() => toggleEnabled(r)} />
      ),
    },
    {
      title: t('更新时间'),
      dataIndex: 'updated_at',
      width: 160,
      render: (v) => (v ? timestamp2string(v) : '-'),
    },
    {
      title: t('操作'),
      width: 160,
      render: (_, r) => (
        <Space>
          <Button size='small' onClick={() => openEdit(r)}>{t('编辑')}</Button>
          <Popconfirm
            title={t('确认删除该关键词？')}
            onConfirm={() => remove(r)}
          >
            <Button size='small' type='danger'>{t('删除')}</Button>
          </Popconfirm>
        </Space>
      ),
    },
  ];

  return (
    <div className='py-2'>
      <Banner
        type='info'
        description={t('命中关键词的请求采样后异步落库；可选是否同时冻结当前 Token，默认仅记录、不冻结。')}
        closeIcon={null}
        className='mb-3'
      />
      <Space className='mb-3'>
        <Input
          placeholder={t('搜索关键词或描述')}
          value={keyword}
          onChange={setKeyword}
          onEnterPress={() => { setPage(1); load(1, keyword); }}
          style={{ width: 240 }}
        />
        <Button onClick={() => { setPage(1); load(1, keyword); }}>{t('搜索')}</Button>
        <Button theme='solid' type='primary' onClick={openCreate}>{t('新增关键词')}</Button>
      </Space>
      <Table
        loading={loading}
        columns={columns}
        dataSource={data}
        rowKey='id'
        pagination={{
          currentPage: page,
          pageSize: PAGE_SIZE,
          total,
          onPageChange: (p) => { setPage(p); load(p, keyword); },
        }}
      />

      <Modal
        title={editing ? t('编辑关键词') : t('新增关键词')}
        visible={modalOpen}
        onCancel={() => setModalOpen(false)}
        onOk={submit}
        okText={t('保存')}
        cancelText={t('取消')}
      >
        <Form labelPosition='left' labelWidth={120}>
          <Form.Slot label={t('关键词')}>
            <Input
              value={form.pattern}
              onChange={(v) => setForm({ ...form, pattern: v })}
              placeholder={t('普通词或正则表达式')}
            />
          </Form.Slot>
          <Form.Slot label={t('正则匹配')}>
            <Switch
              checked={form.is_regex}
              onChange={(v) => setForm({ ...form, is_regex: v })}
            />
            <Typography.Text type='tertiary' className='ml-2'>
              {t('开启后按 Go 正则语法匹配；关闭则按子串匹配（不区分大小写）')}
            </Typography.Text>
          </Form.Slot>
          <Form.Slot label={t('命中冻结 Token')}>
            <Switch
              checked={form.action === ACTION_BLOCK}
              onChange={(v) => setForm({ ...form, action: v ? ACTION_BLOCK : ACTION_MONITOR })}
            />
            <Typography.Text type='tertiary' className='ml-2 block mt-1'>
              {t('默认关闭：命中只记录、不动 Token；开启后命中即把当前 Token 冻结。')}
            </Typography.Text>
          </Form.Slot>
          <Form.Slot label={t('启用')}>
            <Switch
              checked={form.enabled}
              onChange={(v) => setForm({ ...form, enabled: v })}
            />
          </Form.Slot>
          <Form.Slot label={t('描述')}>
            <Input
              value={form.description}
              onChange={(v) => setForm({ ...form, description: v })}
              placeholder={t('可选')}
            />
          </Form.Slot>
        </Form>
      </Modal>
    </div>
  );
};

// ----------- 命中记录 -----------

const BlocksPanel = () => {
  const { t } = useTranslation();
  const [data, setData] = useState([]);
  const [loading, setLoading] = useState(false);
  const [page, setPage] = useState(1);
  const [total, setTotal] = useState(0);
  const [filters, setFilters] = useState({ username: '', model_name: '', ip: '', pattern: '', request_id: '' });
  const [detail, setDetail] = useState(null);

  const buildQuery = (p, f) => {
    const params = new URLSearchParams({ p: String(p), page_size: String(PAGE_SIZE) });
    Object.entries(f).forEach(([k, v]) => {
      if (v != null && String(v).trim() !== '') params.set(k, String(v).trim());
    });
    return params.toString();
  };

  const load = async (p = page, f = filters) => {
    setLoading(true);
    try {
      const res = await API.get(`/api/sensitive_block/?${buildQuery(p, f)}`);
      const { success, message, data } = res.data;
      if (!success) {
        showError(message);
        return;
      }
      setData(data.items || []);
      setTotal(data.total || 0);
    } catch (e) {
      showError(e.message);
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    load(1, filters);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const openDetail = async (record) => {
    try {
      const res = await API.get(`/api/sensitive_block/${record.id}`);
      const { success, message, data } = res.data;
      if (!success) {
        showError(message);
        return;
      }
      setDetail(data);
    } catch (e) {
      showError(e.message);
    }
  };

  // 切换记录关联 Token 的启用/禁用状态
  const toggleToken = async (record, disabled) => {
    if (!record.token_id) {
      showError(t('记录未关联 Token'));
      return;
    }
    try {
      const res = await API.post(`/api/sensitive_block/${record.id}/toggle_token`, { disabled });
      const { success, message } = res.data;
      if (!success) {
        showError(message);
        return;
      }
      showSuccess(disabled ? t('已禁用 Token') : t('已启用 Token'));
      load(page, filters);
    } catch (e) {
      showError(e.message);
    }
  };

  const columns = [
    { title: 'ID', dataIndex: 'id', width: 70 },
    {
      title: t('时间'),
      dataIndex: 'created_at',
      width: 160,
      render: (v) => (v ? timestamp2string(v) : '-'),
    },
    {
      title: t('用户'),
      dataIndex: 'username',
      width: 140,
      render: (v, r) => v || `#${r.user_id}`,
    },
    { title: t('Token'), dataIndex: 'token_name', width: 140 },
    { title: t('模型'), dataIndex: 'model_name', width: 140 },
    {
      title: t('命中关键词'),
      dataIndex: 'matched_pattern',
      width: 200,
      render: (v, r) => (
        <Space>
          <Typography.Text>{v}</Typography.Text>
          {r.is_regex && <Tag color='blue' size='small'>{t('正则')}</Tag>}
        </Space>
      ),
    },
    {
      title: t('命中后行为'),
      dataIndex: 'action',
      width: 140,
      render: (v) => <FreezeTag action={v} t={t} />,
    },
    {
      title: 'IP',
      dataIndex: 'ip',
      width: 140,
      render: (v) => v || '-',
    },
    {
      title: t('Token 状态'),
      dataIndex: 'token_disabled',
      width: 110,
      render: (v) => v
        ? <Tag color='red'>{t('已禁用')}</Tag>
        : <Tag color='green'>{t('正常')}</Tag>,
    },
    {
      title: t('操作'),
      width: 240,
      render: (_, r) => (
        <Space>
          <Button size='small' onClick={() => openDetail(r)}>{t('详情')}</Button>
          {r.token_disabled ? (
            <Popconfirm
              title={t('确认重新启用该 Token？')}
              onConfirm={() => toggleToken(r, false)}
            >
              <Button size='small' theme='solid' type='secondary'>{t('启用 Token')}</Button>
            </Popconfirm>
          ) : (
            <Popconfirm
              title={t('确认禁用该 Token？该用户将无法再用此 Token 调用')}
              onConfirm={() => toggleToken(r, true)}
            >
              <Button size='small' type='danger'>{t('禁用 Token')}</Button>
            </Popconfirm>
          )}
        </Space>
      ),
    },
  ];

  return (
    <div className='py-2'>
      <Space className='mb-3' wrap>
        <Input
          placeholder={t('用户名')}
          value={filters.username}
          onChange={(v) => setFilters({ ...filters, username: v })}
          style={{ width: 140 }}
        />
        <Input
          placeholder={t('模型')}
          value={filters.model_name}
          onChange={(v) => setFilters({ ...filters, model_name: v })}
          style={{ width: 140 }}
        />
        <Input
          placeholder={t('IP')}
          value={filters.ip}
          onChange={(v) => setFilters({ ...filters, ip: v })}
          style={{ width: 140 }}
        />
        <Input
          placeholder={t('命中关键词')}
          value={filters.pattern}
          onChange={(v) => setFilters({ ...filters, pattern: v })}
          style={{ width: 160 }}
        />
        <Input
          placeholder={t('请求 ID')}
          value={filters.request_id}
          onChange={(v) => setFilters({ ...filters, request_id: v })}
          style={{ width: 220 }}
        />
        <Button onClick={() => { setPage(1); load(1, filters); }}>{t('查询')}</Button>
        <Button onClick={() => { const f = { username: '', model_name: '', ip: '', pattern: '', request_id: '' }; setFilters(f); setPage(1); load(1, f); }}>
          {t('重置')}
        </Button>
      </Space>
      <Table
        loading={loading}
        columns={columns}
        dataSource={data}
        rowKey='id'
        pagination={{
          currentPage: page,
          pageSize: PAGE_SIZE,
          total,
          onPageChange: (p) => { setPage(p); load(p, filters); },
        }}
      />
      <DetailModal record={detail} onClose={() => setDetail(null)} onTokenToggle={toggleToken} />
    </div>
  );
};

// ----------- 详情 Modal -----------

const DetailModal = ({ record, onClose, onTokenToggle }) => {
  const { t } = useTranslation();
  const [bodyLoading, setBodyLoading] = useState(false);
  const [bodyData, setBodyData] = useState(null); // { source, body, size, sha256, dump_exists }
  const [bodyError, setBodyError] = useState('');

  // 切换记录时清空 body 缓存
  useEffect(() => {
    setBodyData(null);
    setBodyError('');
    setBodyLoading(false);
  }, [record?.id]);

  if (!record) return null;

  const fetchBody = async () => {
    setBodyLoading(true);
    setBodyError('');
    try {
      const res = await API.get(`/api/sensitive_block/${record.id}/body`);
      const { success, message, data } = res.data;
      if (!success) {
        setBodyError(message || t('读取失败'));
        return;
      }
      setBodyData(data);
    } catch (e) {
      setBodyError(e.message);
    } finally {
      setBodyLoading(false);
    }
  };

  const meta = [
    [t('记录 ID'), record.id],
    [t('请求 ID'), record.request_id || '-'],
    [t('时间'), record.created_at ? timestamp2string(record.created_at) : '-'],
    [t('用户'), record.username ? `${record.username} (#${record.user_id})` : `#${record.user_id}`],
    [t('Token'), record.token_name ? `${record.token_name} (#${record.token_id})` : `#${record.token_id}`],
    [t('渠道'), record.channel_name ? `${record.channel_name} (#${record.channel_id})` : (record.channel_id ? `#${record.channel_id}` : '-')],
    [t('模型'), record.model_name || '-'],
    [t('路径'), record.path || '-'],
    ['IP', record.ip || '-'],
    ['User-Agent', record.user_agent || '-'],
    [t('命中关键词'), `${record.matched_pattern}${record.is_regex ? ' (regex)' : ''}`],
    [t('命中后行为'), record.action === ACTION_BLOCK ? t('记录+冻结Token') : t('仅记录')],
    [t('Token 状态'), record.token_disabled ? t('已禁用') : t('正常')],
    [t('Body 大小'), record.body_size ? `${record.body_size} B` : '-'],
    [t('Body SHA256'), record.body_sha256 || '-'],
    [t('Dump 路径'), record.dump_path || '-'],
    [t('Dump 是否保留'), record.dump_path ? (record.dump_exists ? t('是') : t('已清理')) : '-'],
  ];

  // body 显示分三种状态：
  //   - 未加载：显示按钮 + 提示
  //   - 加载中：spinner
  //   - 已加载：显示 body + 复制按钮 + source tag（dump_file / legacy_db）
  const bodyAvailable = record.dump_path
    ? !!record.dump_exists
    : true; // 旧记录走 legacy_db 兜底；按钮始终可点

  return (
    <Modal
      title={t('命中详情')}
      visible={!!record}
      onCancel={onClose}
      footer={
        <Space>
          {record.token_id > 0 && (
            record.token_disabled ? (
              <Popconfirm
                title={t('确认重新启用该 Token？')}
                onConfirm={() => { onTokenToggle && onTokenToggle(record, false); onClose(); }}
              >
                <Button theme='solid' type='secondary'>{t('启用 Token')}</Button>
              </Popconfirm>
            ) : (
              <Popconfirm
                title={t('确认禁用该 Token？该用户将无法再用此 Token 调用')}
                onConfirm={() => { onTokenToggle && onTokenToggle(record, true); onClose(); }}
              >
                <Button type='danger'>{t('禁用 Token')}</Button>
              </Popconfirm>
            )
          )}
          <Button onClick={onClose}>{t('关闭')}</Button>
        </Space>
      }
      width={760}
    >
      <Typography.Title heading={6}>{t('元数据')}</Typography.Title>
      <table className='w-full text-sm mb-3'>
        <tbody>
          {meta.map(([k, v]) => (
            <tr key={k}>
              <td className='py-1 pr-3 text-gray-500 align-top whitespace-nowrap'>{k}</td>
              <td className='py-1 break-all'>{String(v)}</td>
            </tr>
          ))}
        </tbody>
      </table>

      <Typography.Title heading={6}>{t('命中片段')}</Typography.Title>
      <pre className='bg-gray-50 dark:bg-zinc-800 p-2 rounded text-xs overflow-auto max-h-32 mb-3'>{record.matched_snippet || '-'}</pre>

      <div className='flex items-center justify-between mb-1'>
        <Typography.Title heading={6}>{t('完整请求体')}</Typography.Title>
        <Space>
          {bodyData && bodyData.body && (
            <Button size='small' onClick={() => copy(bodyData.body)}>{t('复制')}</Button>
          )}
          {!bodyData && bodyAvailable && (
            <Button
              size='small'
              theme='solid'
              type='primary'
              loading={bodyLoading}
              onClick={fetchBody}
            >
              {t('加载完整请求体')}
            </Button>
          )}
          {!bodyAvailable && (
            <Tag color='grey'>{t('已清理')}</Tag>
          )}
        </Space>
      </div>
      {bodyData && bodyData.source && (
        <Typography.Text type='tertiary' className='mb-1 block'>
          {bodyData.source === 'dump_file' ? t('来源：本地 dump 文件') : t('来源：存量 DB 记录')}
          {bodyData.size != null ? `  •  ${bodyData.size} B` : ''}
        </Typography.Text>
      )}
      {bodyError && (
        <Typography.Text type='danger' className='mb-1 block'>{bodyError}</Typography.Text>
      )}
      <pre className='bg-gray-50 dark:bg-zinc-800 p-2 rounded text-xs overflow-auto max-h-96'>{
        bodyData ? (bodyData.body || '-') : t('点击「加载完整请求体」按需读取')
      }</pre>
    </Modal>
  );
};

export default SensitiveMonitor;
