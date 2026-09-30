import { useParams } from '@tanstack/react-router'
import { useTranslation } from 'react-i18next'
import { parseCurrencyDisplayType } from '@/lib/currency'
import { useSystemOptions, getOptionValue } from '../hooks/use-system-options'
import type { GeneralSettings } from '../types'
import {
  GENERAL_DEFAULT_SECTION,
  getGeneralSectionContent,
  type GeneralSectionId,
} from './section-registry.tsx'

const defaultGeneralSettings: GeneralSettings = {
  'theme.frontend': 'default',
  Notice: '',
  SystemName: 'New API',
  Logo: '',
  Footer: '',
  About: '',
  HomePageContent: '',
  ServerAddress: '',
  'legal.user_agreement': '',
  'legal.privacy_policy': '',
  QuotaForNewUser: 0,
  PreConsumedQuota: 0,
  QuotaForInviter: 0,
  QuotaForInvitee: 0,
  RewardInviterOnEffectiveOnly: false,
  EffectiveInviteeConsumeThreshold: 0,
  AffiliateCommissionEnabled: false,
  AffiliateCommissionRatio: 0,
  TopUpLink: '',
  QuotaRemindThreshold: '',
  'general_setting.docs_link': '',
  'quota_setting.enable_free_model_pre_consume': true,
  'quota_setting.billing_refund_when_no_output': true,
  'quota_setting.refund_no_output_client_gone_min_seconds': 60,
  'quota_setting.refund_no_output_exclude_upstream_refusal': false,
  QuotaPerUnit: 500000,
  USDExchangeRate: 7,
  'general_setting.quota_display_type': 'USD',
  'general_setting.custom_currency_symbol': '¤',
  'general_setting.custom_currency_exchange_rate': 1,
  DisplayInCurrencyEnabled: true,
  DisplayTokenStatEnabled: true,
  DefaultCollapseSidebar: false,
  DemoSiteEnabled: false,
  SelfUseModeEnabled: false,
  'checkin_setting.enabled': false,
  'checkin_setting.min_quota': 1000,
  'checkin_setting.max_quota': 10000,
}

export function GeneralSettings() {
  const { t } = useTranslation()
  const { data, isLoading } = useSystemOptions()
  const params = useParams({
    from: '/_authenticated/system-settings/general/$section',
  })

  if (isLoading) {
    return (
      <div className='flex items-center justify-center py-12'>
        <div className='text-muted-foreground'>{t('Loading settings...')}</div>
      </div>
    )
  }

  const settings = getOptionValue(data?.data, defaultGeneralSettings)
  const quotaDisplayType = parseCurrencyDisplayType(
    settings['general_setting.quota_display_type']
  )
  // 从注册表推导：手写联合类型曾漏掉 'url-health'，靠 getSectionContent 的 ?? 兜住
  const activeSection = (params?.section ??
    GENERAL_DEFAULT_SECTION) as GeneralSectionId
  const sectionContent = getGeneralSectionContent(
    activeSection,
    settings,
    quotaDisplayType
  )

  return (
    <div className='space-y-4'>{sectionContent}</div>
  )
}
