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

import React, { useCallback, useEffect, useState } from 'react';
import {
  Button,
  Descriptions,
  Modal,
  Space,
  Spin,
  Table,
  Tag,
  Typography,
} from '@douyinfe/semi-ui';
import {
  API,
  showError,
  showSuccess,
  timestamp2string,
} from '../../../../helpers';
import { useTranslation } from 'react-i18next';

const formatLevel = (level) => {
  if (level === -1) return 'Disabled';
  return `L${level ?? 0}`;
};

const healthTag = (snapshot) => {
  if (!snapshot) return <Tag color='grey'>-</Tag>;
  if (snapshot.permanent_disabled === 1) {
    return <Tag color='red'>Locked</Tag>;
  }
  if (snapshot.status === 2 || snapshot.status === 3) {
    return <Tag color='red'>Disabled</Tag>;
  }
  const level = snapshot.degrade_level ?? 0;
  const color =
    level >= 8 ? 'red' : level >= 4 ? 'orange' : level > 0 ? 'yellow' : 'green';
  return <Tag color={color}>{formatLevel(level)}</Tag>;
};

const ChannelHealthModal = ({ visible, channel, onCancel, onRecovered }) => {
  const { t } = useTranslation();
  const [loading, setLoading] = useState(false);
  const [recovering, setRecovering] = useState(false);
  const [snapshot, setSnapshot] = useState(null);
  const [events, setEvents] = useState([]);

  const loadHealth = useCallback(async () => {
    if (!channel?.id) return;
    setLoading(true);
    try {
      const res = await API.get(`/api/channel/${channel.id}/health/events`, {
        params: { days: 30, limit: 200 },
      });
      if (!res.data?.success) {
        showError(res.data?.message || t('加载渠道健康状态失败'));
        return;
      }
      setSnapshot(res.data.data?.snapshot || null);
      setEvents(res.data.data?.events || []);
    } catch (error) {
      showError(error?.message || t('加载渠道健康状态失败'));
    } finally {
      setLoading(false);
    }
  }, [channel?.id, t]);

  useEffect(() => {
    if (visible && channel?.id) {
      setSnapshot(null);
      setEvents([]);
      loadHealth();
    }
  }, [visible, channel?.id, loadHealth]);

  const recover = () => {
    Modal.confirm({
      title: t('恢复渠道健康状态'),
      content: t(
        '将降级级别重置为 L0，恢复原始优先级和权重，并解除自动禁用或永久锁定。',
      ),
      onOk: async () => {
        setRecovering(true);
        try {
          const res = await API.post(
            `/api/channel/${channel.id}/health/recover`,
          );
          if (!res.data?.success) {
            showError(res.data?.message || t('恢复失败'));
            return;
          }
          showSuccess(t('渠道已恢复到 L0'));
          await loadHealth();
          await onRecovered?.();
        } catch (error) {
          showError(error?.message || t('恢复失败'));
        } finally {
          setRecovering(false);
        }
      },
    });
  };

  const columns = [
    {
      title: t('时间'),
      dataIndex: 'created_at',
      width: 170,
      render: (value) => (value ? timestamp2string(value) : '-'),
    },
    {
      title: t('事件'),
      dataIndex: 'event_type',
      width: 100,
      render: (value) => (
        <Tag
          color={value === 'demote' || value === 'disable' ? 'red' : 'green'}
        >
          {value}
        </Tag>
      ),
    },
    {
      title: t('级别'),
      render: (_, record) =>
        `${formatLevel(record.from_level)} → ${formatLevel(record.to_level)}`,
      width: 110,
    },
    { title: t('原因'), dataIndex: 'reason' },
    { title: t('操作方'), dataIndex: 'operator', width: 120 },
  ];

  const descriptionData = snapshot
    ? [
        { key: t('当前健康状态'), value: healthTag(snapshot) },
        { key: t('当前优先级'), value: snapshot.current_priority },
        { key: t('当前权重'), value: snapshot.current_weight },
        { key: t('原始优先级'), value: snapshot.original_priority },
        { key: t('原始权重'), value: snapshot.original_weight },
        {
          key: t('最近降级'),
          value: snapshot.last_demote_at
            ? timestamp2string(snapshot.last_demote_at)
            : '-',
        },
        {
          key: t('最近恢复'),
          value: snapshot.last_upgrade_at
            ? timestamp2string(snapshot.last_upgrade_at)
            : '-',
        },
        { key: t('反弹次数'), value: snapshot.rebounce_count },
        { key: t('最近降级原因'), value: snapshot.last_demote_reason || '-' },
      ]
    : [];

  const alreadyHealthy =
    snapshot?.degrade_level === 0 &&
    snapshot?.permanent_disabled !== 1 &&
    snapshot?.status === 1;

  return (
    <Modal
      title={`${t('渠道健康状态')} · ${channel?.name || ''} (#${channel?.id || '-'})`}
      visible={visible}
      onCancel={onCancel}
      width={860}
      footer={
        <Space>
          <Button onClick={onCancel}>{t('关闭')}</Button>
          <Button onClick={loadHealth} loading={loading}>
            {t('刷新')}
          </Button>
          <Button
            type='danger'
            onClick={recover}
            loading={recovering}
            disabled={!snapshot || alreadyHealthy}
          >
            {t('恢复到 L0')}
          </Button>
        </Space>
      }
    >
      <Spin spinning={loading}>
        {snapshot ? (
          <Descriptions data={descriptionData} row />
        ) : (
          <Typography.Text type='tertiary'>{t('暂无健康快照')}</Typography.Text>
        )}
        {snapshot?.permanent_disabled === 1 && (
          <Typography.Text type='danger'>
            {t('渠道已永久锁定，需要管理员手动恢复。')}
          </Typography.Text>
        )}
        <Typography.Title heading={6} style={{ marginTop: 20 }}>
          {t('最近 30 天健康事件')}
        </Typography.Title>
        <Table
          size='small'
          columns={columns}
          dataSource={events}
          rowKey='id'
          pagination={{ pageSize: 10 }}
          empty={t('暂无健康事件')}
        />
      </Spin>
    </Modal>
  );
};

export default ChannelHealthModal;
