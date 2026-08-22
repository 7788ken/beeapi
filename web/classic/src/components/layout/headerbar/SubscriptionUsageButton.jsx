import React from 'react';
import { Button, Divider, Popover, Typography } from '@douyinfe/semi-ui';
import { Coins, RefreshCw } from 'lucide-react';
import { useSubscriptionSummary } from '../../../hooks/common/useSubscriptionSummary';
import SubscriptionUsageProgress from '../../topup/SubscriptionUsageProgress';

const { Text } = Typography;

const SubscriptionUsageButton = ({ t, userState }) => {
  const { subscriptions, loading, refresh } = useSubscriptionSummary();

  if (!userState?.user) return null;
  if (subscriptions.length === 0 && !loading) return null;

  const primary = subscriptions[0];
  const total = Number(primary?.subscription?.amount_total || 0);
  const used = Number(primary?.subscription?.amount_used || 0);
  const percent =
    total > 0 ? Math.min(100, Math.round((used / total) * 100)) : 0;
  const triggerLabel = total > 0 ? `${percent}%` : t('订阅');

  const popoverContent = (
    <div className='w-72 max-w-[80vw] py-1'>
      <div className='flex items-center justify-between mb-2 px-1'>
        <Text strong>{t('订阅额度')}</Text>
        <Button
          size='small'
          theme='borderless'
          type='tertiary'
          icon={
            <RefreshCw
              size={12}
              className={loading ? 'animate-spin' : ''}
            />
          }
          onClick={refresh}
          loading={loading}
          aria-label={t('刷新')}
        />
      </div>
      <Divider margin={4} />
      {subscriptions.length === 0 ? (
        <div className='text-xs text-gray-500 py-2 px-1'>
          {t('当前没有生效中的订阅')}
        </div>
      ) : (
        <div className='space-y-3 max-h-72 overflow-y-auto px-1 pt-2'>
          {subscriptions.map((sub) => (
            <div key={sub?.subscription?.id}>
              <div className='text-xs font-medium mb-1 truncate'>
                {sub?.plan?.title ||
                  `${t('订阅')} #${sub?.subscription?.id}`}
              </div>
              <SubscriptionUsageProgress
                t={t}
                subscription={sub.subscription}
                size='small'
              />
            </div>
          ))}
        </div>
      )}
    </div>
  );

  return (
    <Popover content={popoverContent} trigger='click' position='bottomRight'>
      <Button
        aria-label={t('订阅额度')}
        icon={<Coins size={16} />}
        theme='borderless'
        type='tertiary'
        className='!px-2 !py-1.5 !text-current focus:!bg-semi-color-fill-1 dark:focus:!bg-gray-700 !rounded-full !bg-semi-color-fill-0 dark:!bg-semi-color-fill-1 hover:!bg-semi-color-fill-1 dark:hover:!bg-semi-color-fill-2'
      >
        <span className='ml-1 text-xs hidden sm:inline'>{triggerLabel}</span>
      </Button>
    </Popover>
  );
};

export default SubscriptionUsageButton;
