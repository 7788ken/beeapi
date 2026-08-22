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

import React, { useMemo, useState } from 'react';
import {
  Badge,
  Button,
  Card,
  Divider,
  Select,
  Skeleton,
  Space,
  Tabs,
  TabPane,
  Tag,
  Tooltip,
  Typography,
} from '@douyinfe/semi-ui';
import { API, showError, showSuccess, renderQuota } from '../../helpers';
import { getCurrencyConfig } from '../../helpers/render';
import { RefreshCw, Sparkles } from 'lucide-react';
import SubscriptionPurchaseModal from './modals/SubscriptionPurchaseModal';
import SubscriptionUsageProgress from './SubscriptionUsageProgress';
import {
  formatSubscriptionDuration,
  formatSubscriptionResetPeriod,
} from '../../helpers/subscriptionFormat';

const { Text } = Typography;

// 过滤易支付方式
function getEpayMethods(payMethods = []) {
  return (payMethods || []).filter(
    (m) => m?.type && m.type !== 'stripe' && m.type !== 'creem',
  );
}

// 提交易支付表单
function submitEpayForm({ url, params }) {
  const form = document.createElement('form');
  form.action = url;
  form.method = 'POST';
  const isSafari =
    navigator.userAgent.indexOf('Safari') > -1 &&
    navigator.userAgent.indexOf('Chrome') < 1;
  if (!isSafari) form.target = '_blank';
  Object.keys(params || {}).forEach((key) => {
    const input = document.createElement('input');
    input.type = 'hidden';
    input.name = key;
    input.value = params[key];
    form.appendChild(input);
  });
  document.body.appendChild(form);
  form.submit();
  document.body.removeChild(form);
}

const SubscriptionPlansCard = ({
  t,
  loading = false,
  plans = [],
  payMethods = [],
  enableOnlineTopUp = false,
  enableStripeTopUp = false,
  enableCreemTopUp = false,
  billingPreference,
  onChangeBillingPreference,
  activeSubscriptions = [],
  allSubscriptions = [],
  reloadSubscriptionSelf,
  withCard = true,
}) => {
  const [open, setOpen] = useState(false);
  const [selectedPlan, setSelectedPlan] = useState(null);
  const [paying, setPaying] = useState(false);
  const [selectedEpayMethod, setSelectedEpayMethod] = useState('');
  const [refreshing, setRefreshing] = useState(false);

  const epayMethods = useMemo(() => getEpayMethods(payMethods), [payMethods]);

  const openBuy = (p) => {
    setSelectedPlan(p);
    setSelectedEpayMethod(epayMethods?.[0]?.type || '');
    setOpen(true);
  };

  const closeBuy = () => {
    setOpen(false);
    setSelectedPlan(null);
    setPaying(false);
  };

  const handleRefresh = async () => {
    setRefreshing(true);
    try {
      await reloadSubscriptionSelf?.();
    } finally {
      setRefreshing(false);
    }
  };

  const payStripe = async () => {
    if (!selectedPlan?.plan?.stripe_price_id) {
      showError(t('该套餐未配置 Stripe'));
      return;
    }
    setPaying(true);
    try {
      const res = await API.post('/api/subscription/stripe/pay', {
        plan_id: selectedPlan.plan.id,
      });
      if (res.data?.message === 'success') {
        window.open(res.data.data?.pay_link, '_blank');
        showSuccess(t('已打开支付页面'));
        closeBuy();
      } else {
        const errorMsg =
          typeof res.data?.data === 'string'
            ? res.data.data
            : res.data?.message || t('支付失败');
        showError(errorMsg);
      }
    } catch (e) {
      showError(t('支付请求失败'));
    } finally {
      setPaying(false);
    }
  };

  const payCreem = async () => {
    if (!selectedPlan?.plan?.creem_product_id) {
      showError(t('该套餐未配置 Creem'));
      return;
    }
    setPaying(true);
    try {
      const res = await API.post('/api/subscription/creem/pay', {
        plan_id: selectedPlan.plan.id,
      });
      if (res.data?.message === 'success') {
        window.open(res.data.data?.checkout_url, '_blank');
        showSuccess(t('已打开支付页面'));
        closeBuy();
      } else {
        const errorMsg =
          typeof res.data?.data === 'string'
            ? res.data.data
            : res.data?.message || t('支付失败');
        showError(errorMsg);
      }
    } catch (e) {
      showError(t('支付请求失败'));
    } finally {
      setPaying(false);
    }
  };

  const payEpay = async () => {
    if (!selectedEpayMethod) {
      showError(t('请选择支付方式'));
      return;
    }
    setPaying(true);
    try {
      const res = await API.post('/api/subscription/epay/pay', {
        plan_id: selectedPlan.plan.id,
        payment_method: selectedEpayMethod,
      });
      if (res.data?.message === 'success') {
        submitEpayForm({ url: res.data.url, params: res.data.data });
        showSuccess(t('已发起支付'));
        closeBuy();
      } else {
        const errorMsg =
          typeof res.data?.data === 'string'
            ? res.data.data
            : res.data?.message || t('支付失败');
        showError(errorMsg);
      }
    } catch (e) {
      showError(t('支付请求失败'));
    } finally {
      setPaying(false);
    }
  };

  // 当前订阅信息 - 支持多个订阅
  const hasActiveSubscription = activeSubscriptions.length > 0;
  const disableSubscriptionPreference = !hasActiveSubscription;
  const isSubscriptionPreference =
    billingPreference === 'subscription_first' ||
    billingPreference === 'subscription_then_auto' ||
    billingPreference === 'subscription_only';
  const displayBillingPreference =
    disableSubscriptionPreference && isSubscriptionPreference
      ? 'wallet_first'
      : billingPreference;
  const subscriptionPreferenceLabel =
    billingPreference === 'subscription_only'
      ? t('仅用订阅')
      : billingPreference === 'subscription_then_auto'
        ? t('优先订阅（用完转按量）')
        : t('优先订阅');

  const planPurchaseCountMap = useMemo(() => {
    const map = new Map();
    (allSubscriptions || []).forEach((sub) => {
      const planId = sub?.subscription?.plan_id;
      if (!planId) return;
      map.set(planId, (map.get(planId) || 0) + 1);
    });
    return map;
  }, [allSubscriptions]);

  const planTitleMap = useMemo(() => {
    const map = new Map();
    (plans || []).forEach((p) => {
      const plan = p?.plan;
      if (!plan?.id) return;
      map.set(plan.id, plan.title || '');
    });
    return map;
  }, [plans]);

  const planBoundGroupMap = useMemo(() => {
    const map = new Map();
    (plans || []).forEach((p) => {
      const plan = p?.plan;
      if (!plan?.id) return;
      map.set(plan.id, plan.bound_group || '');
    });
    return map;
  }, [plans]);

  // 将订阅按状态分组：生效中 / 近 30 天内过期或作废
  const { activeSubsList, expiredRecentList } = useMemo(() => {
    const THIRTY_DAYS = 30 * 86400;
    const now = Date.now() / 1000;
    const active = [];
    const expired = [];
    (allSubscriptions || []).forEach((sub) => {
      const endTime = Number(sub?.subscription?.end_time || 0);
      const status = sub?.subscription?.status;
      const isExpired = endTime > 0 && endTime <= now;
      const isCancelled = status === 'cancelled';
      if ((status === 'active' || status === 'exhausted') && !isExpired) {
        active.push(sub);
        return;
      }
      if (isCancelled || isExpired) {
        if (endTime > 0 && now - endTime <= THIRTY_DAYS) expired.push(sub);
      }
    });
    return { activeSubsList: active, expiredRecentList: expired };
  }, [allSubscriptions]);

  const [mySubsTab, setMySubsTab] = useState('active');

  const getPlanPurchaseCount = (planId) =>
    planPurchaseCountMap.get(planId) || 0;

  // 计算单个订阅的剩余天数
  const getRemainingDays = (sub) => {
    if (!sub?.subscription?.end_time) return 0;
    const now = Date.now() / 1000;
    const remaining = sub.subscription.end_time - now;
    return Math.max(0, Math.ceil(remaining / 86400));
  };

  const cardContent = (
    <>
      {/* 卡片头部 */}
      {loading ? (
        <div className='space-y-4'>
          {/* 我的订阅骨架屏 */}
          <Card className='!rounded-xl w-full' bodyStyle={{ padding: '12px' }}>
            <div className='flex items-center justify-between mb-3'>
              <Skeleton.Title active style={{ width: 100, height: 20 }} />
              <Skeleton.Button active style={{ width: 24, height: 24 }} />
            </div>
            <div className='space-y-2'>
              <Skeleton.Paragraph active rows={2} />
            </div>
          </Card>
          {/* 套餐列表骨架屏 */}
          <div className='grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-2 xl:grid-cols-3 gap-5 w-full px-1'>
            {[1, 2, 3].map((i) => (
              <Card
                key={i}
                className='!rounded-xl w-full h-full'
                bodyStyle={{ padding: 16 }}
              >
                <Skeleton.Title
                  active
                  style={{ width: '60%', height: 24, marginBottom: 8 }}
                />
                <Skeleton.Paragraph
                  active
                  rows={1}
                  style={{ marginBottom: 12 }}
                />
                <div className='text-center py-4'>
                  <Skeleton.Title
                    active
                    style={{ width: '40%', height: 32, margin: '0 auto' }}
                  />
                </div>
                <Skeleton.Paragraph active rows={3} style={{ marginTop: 12 }} />
                <Skeleton.Button
                  active
                  block
                  style={{ marginTop: 16, height: 32 }}
                />
              </Card>
            ))}
          </div>
        </div>
      ) : (
        <Space vertical style={{ width: '100%' }} spacing={8}>
          {/* 当前订阅状态 */}
          <Card className='!rounded-xl w-full' bodyStyle={{ padding: '12px' }}>
            <div className='flex items-center justify-between mb-2 gap-3'>
              <div className='flex items-center gap-2 flex-1 min-w-0'>
                <Text strong>{t('我的订阅')}</Text>
              </div>
              <div className='flex items-center gap-2'>
                <Select
                  value={displayBillingPreference}
                  onChange={onChangeBillingPreference}
                  size='small'
                  optionList={[
                    {
                      value: 'subscription_first',
                      label: disableSubscriptionPreference
                        ? `${t('优先订阅')} (${t('无生效')})`
                        : t('优先订阅'),
                      disabled: disableSubscriptionPreference,
                    },
                    {
                      value: 'subscription_then_auto',
                      label: disableSubscriptionPreference
                        ? `${t('优先订阅（用完转按量）')} (${t('无生效')})`
                        : t('优先订阅（用完转按量）'),
                      disabled: disableSubscriptionPreference,
                    },
                    { value: 'wallet_first', label: t('优先钱包') },
                    {
                      value: 'subscription_only',
                      label: disableSubscriptionPreference
                        ? `${t('仅用订阅')} (${t('无生效')})`
                        : t('仅用订阅'),
                      disabled: disableSubscriptionPreference,
                    },
                    { value: 'wallet_only', label: t('仅用钱包') },
                  ]}
                />
                <Button
                  size='small'
                  theme='light'
                  type='tertiary'
                  icon={
                    <RefreshCw
                      size={12}
                      className={refreshing ? 'animate-spin' : ''}
                    />
                  }
                  onClick={handleRefresh}
                  loading={refreshing}
                />
              </div>
            </div>
            {disableSubscriptionPreference && isSubscriptionPreference && (
              <Text type='tertiary' size='small'>
                {t('已保存偏好为')}
                {subscriptionPreferenceLabel}
                {t('，当前无生效订阅，将自动使用钱包')}
              </Text>
            )}

            {(() => {
              const renderSubItem = (sub, subIndex, total, tabKind) => {
                const isLast = subIndex === total - 1;
                const subscription = sub.subscription;
                const planTitle =
                  planTitleMap.get(subscription?.plan_id) || '';
                const boundGroup =
                  sub?.plan?.bound_group ||
                  planBoundGroupMap.get(subscription?.plan_id) ||
                  '';
                const remainDays = getRemainingDays(sub);
                const isCancelled = subscription?.status === 'cancelled';
                const isExhausted = subscription?.status === 'exhausted';
                const isActiveItem = tabKind === 'active';

                return (
                  <div key={subscription?.id || subIndex}>
                    <div className='flex items-center justify-between text-xs mb-2'>
                      <div className='flex items-center gap-2'>
                        <span className='font-medium'>
                          {planTitle
                            ? `${planTitle} · ${t('订阅')} #${subscription?.id}`
                            : `${t('订阅')} #${subscription?.id}`}
                        </span>
                        {isActiveItem && isExhausted ? (
                          <Tag
                            color='white'
                            size='small'
                            shape='circle'
                            prefixIcon={<Badge dot type='warning' />}
                          >
                            {t('额度已用尽')}
                          </Tag>
                        ) : isActiveItem ? (
                          <Tag
                            color='white'
                            size='small'
                            shape='circle'
                            prefixIcon={<Badge dot type='success' />}
                          >
                            {t('生效')}
                          </Tag>
                        ) : isCancelled ? (
                          <Tag color='white' size='small' shape='circle'>
                            {t('已作废')}
                          </Tag>
                        ) : (
                          <Tag color='white' size='small' shape='circle'>
                            {t('已过期')}
                          </Tag>
                        )}
                      </div>
                      {isActiveItem && (
                        <span className='text-gray-500'>
                          {t('剩余')} {remainDays} {t('天')}
                        </span>
                      )}
                    </div>
                    <div className='text-xs text-gray-500 mb-2'>
                      {isActiveItem
                        ? t('至')
                        : isCancelled
                          ? t('作废于')
                          : t('过期于')}{' '}
                      {new Date(
                        (subscription?.end_time || 0) * 1000,
                      ).toLocaleString()}
                    </div>
                    {isActiveItem && subscription?.next_reset_time > 0 && (
                      <div className='text-xs text-gray-500 mb-2'>
                        {t('下一次重置')}:{' '}
                        {new Date(
                          subscription.next_reset_time * 1000,
                        ).toLocaleString()}
                      </div>
                    )}
                    {boundGroup && (
                      <div className='text-xs text-blue-600 mb-2'>
                        {t('本订阅仅在 {{group}} 渠道扣费', {
                          group: boundGroup,
                        })}
                      </div>
                    )}
                    <div className='mb-2'>
                      <SubscriptionUsageProgress
                        t={t}
                        subscription={subscription}
                        size='small'
                      />
                    </div>
                    {!isLast && <Divider margin={12} />}
                  </div>
                );
              };

              const activeCount = activeSubsList.length;
              const expiredCount = expiredRecentList.length;

              if (activeCount === 0 && expiredCount === 0) {
                return (
                  <div className='text-xs text-gray-500 mt-2'>
                    {t('购买套餐后即可享受模型权益')}
                  </div>
                );
              }

              return (
                <>
                  <Divider margin={8} />
                  <Tabs
                    type='line'
                    size='small'
                    activeKey={mySubsTab}
                    onChange={setMySubsTab}
                  >
                    <TabPane
                      tab={`${t('有效')} (${activeCount})`}
                      itemKey='active'
                    >
                      {activeCount > 0 ? (
                        <div className='max-h-64 overflow-y-auto pr-1 pt-2 semi-table-body'>
                          {activeSubsList.map((sub, idx) =>
                            renderSubItem(sub, idx, activeCount, 'active'),
                          )}
                        </div>
                      ) : (
                        <div className='text-xs text-gray-500 py-3'>
                          {t('当前没有生效中的订阅')}
                        </div>
                      )}
                    </TabPane>
                    <TabPane
                      tab={`${t('过期')} (${expiredCount})`}
                      itemKey='expired'
                    >
                      {expiredCount > 0 ? (
                        <>
                          <div className='max-h-64 overflow-y-auto pr-1 pt-2 semi-table-body'>
                            {expiredRecentList.map((sub, idx) =>
                              renderSubItem(sub, idx, expiredCount, 'expired'),
                            )}
                          </div>
                          <div className='text-[11px] text-gray-400 mt-2'>
                            {t('仅显示最近 30 天内过期/作废的订阅')}
                          </div>
                        </>
                      ) : (
                        <div className='text-xs text-gray-500 py-3'>
                          {t('近 30 天内没有过期的订阅')}
                        </div>
                      )}
                    </TabPane>
                  </Tabs>
                </>
              );
            })()}
          </Card>

          {/* 可购买套餐 - 标准定价卡片 */}
          {plans.length > 0 ? (
            <div className='grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-2 xl:grid-cols-3 gap-5 w-full px-1'>
              {plans.map((p, index) => {
                const plan = p?.plan;
                const totalAmount = Number(plan?.total_amount || 0);
                const { symbol, rate } = getCurrencyConfig();
                const price = Number(plan?.price_amount || 0);
                const convertedPrice = price * rate;
                const displayPrice = convertedPrice.toFixed(
                  Number.isInteger(convertedPrice) ? 0 : 2,
                );
                const isPopular = index === 0 && plans.length > 1;
                const limit = Number(plan?.max_purchase_per_user || 0);
                const limitLabel = limit > 0 ? `${t('限购')} ${limit}` : null;
                const totalLabel =
                  totalAmount > 0
                    ? `${t('总额度')}: ${renderQuota(totalAmount)}`
                    : `${t('总额度')}: ${t('不限')}`;
                const upgradeLabel = plan?.upgrade_group
                  ? `${t('升级分组')}: ${plan.upgrade_group}`
                  : null;
                const resetLabel =
                  formatSubscriptionResetPeriod(plan, t) === t('不重置')
                    ? null
                    : `${t('额度重置')}: ${formatSubscriptionResetPeriod(plan, t)}`;
                const planBenefits = [
                  {
                    label: `${t('有效期')}: ${formatSubscriptionDuration(plan, t)}`,
                  },
                  resetLabel ? { label: resetLabel } : null,
                  totalAmount > 0
                    ? {
                        label: totalLabel,
                        tooltip: `${t('原生额度')}：${totalAmount}`,
                      }
                    : { label: totalLabel },
                  limitLabel ? { label: limitLabel } : null,
                  upgradeLabel ? { label: upgradeLabel } : null,
                ].filter(Boolean);

                return (
                  <Card
                    key={plan?.id}
                    className={`!rounded-xl transition-all hover:shadow-lg w-full h-full ${
                      isPopular ? 'ring-2 ring-purple-500' : ''
                    }`}
                    bodyStyle={{ padding: 0 }}
                  >
                    <div className='p-4 h-full flex flex-col'>
                      {/* 推荐标签 */}
                      {isPopular && (
                        <div className='mb-2'>
                          <Tag color='purple' shape='circle' size='small'>
                            <Sparkles size={10} className='mr-1' />
                            {t('推荐')}
                          </Tag>
                        </div>
                      )}
                      {/* 套餐名称 */}
                      <div className='mb-3'>
                        <Typography.Title
                          heading={5}
                          ellipsis={{ rows: 1, showTooltip: true }}
                          style={{ margin: 0 }}
                        >
                          {plan?.title || t('订阅套餐')}
                        </Typography.Title>
                        {plan?.subtitle && (
                          <Text
                            type='tertiary'
                            size='small'
                            ellipsis={{ rows: 1, showTooltip: true }}
                            style={{ display: 'block' }}
                          >
                            {plan.subtitle}
                          </Text>
                        )}
                      </div>

                      {/* 价格区域 */}
                      <div className='py-2'>
                        <div className='flex items-baseline justify-start'>
                          <span className='text-xl font-bold text-purple-600'>
                            {symbol}
                          </span>
                          <span className='text-3xl font-bold text-purple-600'>
                            {displayPrice}
                          </span>
                        </div>
                      </div>

                      {/* 套餐权益描述 */}
                      <div className='flex flex-col items-start gap-1 pb-2'>
                        {planBenefits.map((item) => {
                          const content = (
                            <div className='flex items-center gap-2 text-xs text-gray-500'>
                              <Badge dot type='tertiary' />
                              <span>{item.label}</span>
                            </div>
                          );
                          if (!item.tooltip) {
                            return (
                              <div
                                key={item.label}
                                className='w-full flex justify-start'
                              >
                                {content}
                              </div>
                            );
                          }
                          return (
                            <Tooltip key={item.label} content={item.tooltip}>
                              <div className='w-full flex justify-start'>
                                {content}
                              </div>
                            </Tooltip>
                          );
                        })}
                      </div>

                      <div className='mt-auto'>
                        <Divider margin={12} />

                        {/* 购买按钮 */}
                        {(() => {
                          const count = getPlanPurchaseCount(p?.plan?.id);
                          const reached = limit > 0 && count >= limit;
                          const tip = reached
                            ? t('已达到购买上限') + ` (${count}/${limit})`
                            : '';
                          const buttonEl = (
                            <Button
                              theme='outline'
                              type='primary'
                              block
                              disabled={reached}
                              onClick={() => {
                                if (!reached) openBuy(p);
                              }}
                            >
                              {reached ? t('已达上限') : t('立即订阅')}
                            </Button>
                          );
                          return reached ? (
                            <Tooltip content={tip} position='top'>
                              {buttonEl}
                            </Tooltip>
                          ) : (
                            buttonEl
                          );
                        })()}
                      </div>
                    </div>
                  </Card>
                );
              })}
            </div>
          ) : (
            <div className='text-center text-gray-400 text-sm py-4'>
              {t('暂无可购买套餐')}
            </div>
          )}
        </Space>
      )}
    </>
  );

  return (
    <>
      {withCard ? (
        <Card className='!rounded-2xl shadow-sm border-0'>{cardContent}</Card>
      ) : (
        <div className='space-y-3'>{cardContent}</div>
      )}

      {/* 购买确认弹窗 */}
      <SubscriptionPurchaseModal
        t={t}
        visible={open}
        onCancel={closeBuy}
        selectedPlan={selectedPlan}
        paying={paying}
        selectedEpayMethod={selectedEpayMethod}
        setSelectedEpayMethod={setSelectedEpayMethod}
        epayMethods={epayMethods}
        enableOnlineTopUp={enableOnlineTopUp}
        enableStripeTopUp={enableStripeTopUp}
        enableCreemTopUp={enableCreemTopUp}
        purchaseLimitInfo={
          selectedPlan?.plan?.id
            ? {
                limit: Number(selectedPlan?.plan?.max_purchase_per_user || 0),
                count: getPlanPurchaseCount(selectedPlan?.plan?.id),
              }
            : null
        }
        onPayStripe={payStripe}
        onPayCreem={payCreem}
        onPayEpay={payEpay}
      />
    </>
  );
};

export default SubscriptionPlansCard;
