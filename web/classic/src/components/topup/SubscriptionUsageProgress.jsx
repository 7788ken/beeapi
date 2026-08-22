import React from 'react';
import { Progress, Tooltip, Typography } from '@douyinfe/semi-ui';
import { renderQuota } from '../../helpers';

const { Text } = Typography;

const getProgressColor = (pct) => {
  if (pct < 60) return 'var(--semi-color-success)';
  if (pct < 85) return 'var(--semi-color-warning)';
  return 'var(--semi-color-danger)';
};

const SubscriptionUsageProgress = ({
  t,
  subscription,
  showLabel = true,
  size = 'default',
}) => {
  const total = Number(subscription?.amount_total || 0);
  const used = Number(subscription?.amount_used || 0);
  const isSmall = size === 'small';

  if (total <= 0) {
    if (!showLabel) return null;
    return (
      <Text type='tertiary' size='small'>
        {t('总额度')}: {t('不限')}
      </Text>
    );
  }

  const remain = Math.max(0, total - used);
  const percent = Math.min(100, Math.round((used / total) * 100));
  const color = getProgressColor(percent);

  return (
    <div className={isSmall ? 'space-y-1' : 'space-y-1.5'}>
      {showLabel && (
        <div
          className={`flex items-center justify-between ${
            isSmall ? 'text-[11px]' : 'text-xs'
          } text-gray-500`}
        >
          <Tooltip
            content={`${t('原生额度')}：${used}/${total} · ${t('剩余')} ${remain}`}
          >
            <span>
              {renderQuota(used)}/{renderQuota(total)} · {t('剩余')}{' '}
              {renderQuota(remain)}
            </span>
          </Tooltip>
          <span className='ml-2 font-medium' style={{ color }}>
            {percent}%
          </span>
        </div>
      )}
      <Progress
        percent={percent}
        stroke={color}
        size={isSmall ? 'small' : 'default'}
        showInfo={false}
        aria-label='subscription quota usage'
      />
    </div>
  );
};

export default SubscriptionUsageProgress;
