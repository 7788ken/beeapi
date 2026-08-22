import type { IntegrationSettings } from '../types'
import { createSectionRegistry } from '../utils/section-registry'
import { AgouSettingsSection } from './agou-settings-section'
import { EmailSettingsSection } from './email-settings-section'
import { IoNetDeploymentSettingsSection } from './ionet-deployment-settings-section'
import { MonitoringSettingsSection } from './monitoring-settings-section'
import { PaymentSettingsSection } from './payment-settings-section'
import { ReconcileBillSettingsSection } from './reconcile-bill-settings-section'
import { SubSiteSettingsSection } from './sub-site-settings-section'
import { WorkerSettingsSection } from './worker-settings-section'

const INTEGRATIONS_SECTIONS = [
  {
    id: 'payment',
    titleKey: 'Payment Gateway',
    descriptionKey: 'Configure payment gateway integrations',
    build: (settings: IntegrationSettings) => (
      <PaymentSettingsSection
        defaultValues={{
          PayAddress: settings.PayAddress,
          EpayId: settings.EpayId,
          EpayKey: settings.EpayKey,
          Price: settings.Price,
          MinTopUp: settings.MinTopUp,
          CustomCallbackAddress: settings.CustomCallbackAddress,
          PayMethods: settings.PayMethods,
          AmountOptions: settings['payment_setting.amount_options'],
          AmountDiscount: settings['payment_setting.amount_discount'],
          StripeApiSecret: settings.StripeApiSecret,
          StripeWebhookSecret: settings.StripeWebhookSecret,
          StripePriceId: settings.StripePriceId,
          StripeUnitPrice: settings.StripeUnitPrice,
          StripeMinTopUp: settings.StripeMinTopUp,
          StripePromotionCodesEnabled: settings.StripePromotionCodesEnabled,
          CreemApiKey: settings.CreemApiKey,
          CreemWebhookSecret: settings.CreemWebhookSecret,
          CreemTestMode: settings.CreemTestMode,
          CreemProducts: settings.CreemProducts,
        }}
        waffoDefaultValues={{
          WaffoEnabled: settings.WaffoEnabled ?? false,
          WaffoApiKey: settings.WaffoApiKey ?? '',
          WaffoPrivateKey: settings.WaffoPrivateKey ?? '',
          WaffoPublicCert: settings.WaffoPublicCert ?? '',
          WaffoSandboxPublicCert: settings.WaffoSandboxPublicCert ?? '',
          WaffoSandboxApiKey: settings.WaffoSandboxApiKey ?? '',
          WaffoSandboxPrivateKey: settings.WaffoSandboxPrivateKey ?? '',
          WaffoSandbox: settings.WaffoSandbox ?? false,
          WaffoMerchantId: settings.WaffoMerchantId ?? '',
          WaffoCurrency: settings.WaffoCurrency ?? 'USD',
          WaffoUnitPrice: settings.WaffoUnitPrice ?? 1,
          WaffoMinTopUp: settings.WaffoMinTopUp ?? 1,
          WaffoNotifyUrl: settings.WaffoNotifyUrl ?? '',
          WaffoReturnUrl: settings.WaffoReturnUrl ?? '',
          WaffoPayMethods: settings.WaffoPayMethods ?? '[]',
          WaffoAllowedGroups: settings.WaffoAllowedGroups ?? '',
        }}
        waffoPancakeDefaultValues={{
          WaffoPancakeEnabled: settings.WaffoPancakeEnabled ?? false,
          WaffoPancakeSandbox: settings.WaffoPancakeSandbox ?? false,
          WaffoPancakeMerchantID: settings.WaffoPancakeMerchantID ?? '',
          WaffoPancakePrivateKey: settings.WaffoPancakePrivateKey ?? '',
          WaffoPancakeWebhookPublicKey:
            settings.WaffoPancakeWebhookPublicKey ?? '',
          WaffoPancakeWebhookTestKey: settings.WaffoPancakeWebhookTestKey ?? '',
          WaffoPancakeStoreID: settings.WaffoPancakeStoreID ?? '',
          WaffoPancakeProductID: settings.WaffoPancakeProductID ?? '',
          WaffoPancakeReturnURL: settings.WaffoPancakeReturnURL ?? '',
          WaffoPancakeCurrency: settings.WaffoPancakeCurrency ?? 'USD',
          WaffoPancakeUnitPrice: settings.WaffoPancakeUnitPrice ?? 1,
          WaffoPancakeMinTopUp: settings.WaffoPancakeMinTopUp ?? 1,
          WaffoPancakeAllowedGroups: settings.WaffoPancakeAllowedGroups ?? '',
          WaffoPancakePayChannels: settings.WaffoPancakePayChannels ?? '[]',
          WaffoPancakeLogo: settings.WaffoPancakeLogo ?? '',
        }}
        cryptomusDefaultValues={{
          CryptomusEnabled: settings.CryptomusEnabled ?? false,
          CryptomusMerchantID: settings.CryptomusMerchantID ?? '',
          CryptomusPaymentApiKey: settings.CryptomusPaymentApiKey ?? '',
          CryptomusWebhookApiKey: settings.CryptomusWebhookApiKey ?? '',
          CryptomusDefaultCurrency:
            settings.CryptomusDefaultCurrency ?? 'USDT',
          CryptomusDefaultNetwork: settings.CryptomusDefaultNetwork ?? 'TRX',
          CryptomusAllowedCurrencies:
            settings.CryptomusAllowedCurrencies ?? '',
          CryptomusUnitPrice: settings.CryptomusUnitPrice ?? 1,
          CryptomusMinTopUp: settings.CryptomusMinTopUp ?? 1,
          CryptomusReturnURL: settings.CryptomusReturnURL ?? '',
          CryptomusAllowedGroups: settings.CryptomusAllowedGroups ?? '',
          CryptomusPayChannels: settings.CryptomusPayChannels ?? '[]',
          CryptomusLogo: settings.CryptomusLogo ?? '',
        }}
      />
    ),
  },
  {
    id: 'sfpay',
    titleKey: 'Sfpay Payment Gateway',
    descriptionKey: 'Configure sfpay payment gateway integration',
    build: (settings: IntegrationSettings) => (
      <AgouSettingsSection
        defaultValues={{
          SfpayEnabled: settings.SfpayEnabled ?? false,
          SfpayBaseURL: settings.SfpayBaseURL ?? '',
          SfpayAppId: settings.SfpayAppId ?? '',
          SfpayAppSecret: settings.SfpayAppSecret ?? '',
          SfpayGroupCode: settings.SfpayGroupCode ?? '',
          SfpayNotifyUrl: settings.SfpayNotifyUrl ?? '',
          SfpayReturnUrl: settings.SfpayReturnUrl ?? '',
          SfpayUnitPrice: settings.SfpayUnitPrice ?? 7.3,
          SfpayMinTopUp: settings.SfpayMinTopUp ?? 1,
          SfpayMaxTopUp: settings.SfpayMaxTopUp ?? 0,
          SfpayAllowedCallbackIPs:
            settings.SfpayAllowedCallbackIPs ?? '',
          SfpayAlipayPayType: settings.SfpayAlipayPayType ?? 'ZFBPAY',
          SfpayWechatEnabled: settings.SfpayWechatEnabled ?? false,
          SfpayWechatPayType: settings.SfpayWechatPayType ?? '',
          SfpayAllowedGroups: settings.SfpayAllowedGroups ?? '',
          SfpayPayChannels: settings.SfpayPayChannels ?? '[]',
          SfpayLogo: settings.SfpayLogo ?? '',
        }}
      />
    ),
  },
  {
    id: 'email',
    titleKey: 'SMTP Email',
    descriptionKey: 'Configure SMTP email settings',
    build: (settings: IntegrationSettings) => (
      <EmailSettingsSection
        defaultValues={{
          SMTPServer: settings.SMTPServer,
          SMTPPort: settings.SMTPPort,
          SMTPAccount: settings.SMTPAccount,
          SMTPFrom: settings.SMTPFrom,
          SMTPToken: settings.SMTPToken,
          SMTPSSLEnabled: settings.SMTPSSLEnabled,
          SMTPForceAuthLogin: settings.SMTPForceAuthLogin,
        }}
      />
    ),
  },
  {
    id: 'worker',
    titleKey: 'Worker Proxy',
    descriptionKey: 'Configure worker service settings',
    build: (settings: IntegrationSettings) => (
      <WorkerSettingsSection
        defaultValues={{
          WorkerUrl: settings.WorkerUrl,
          WorkerValidKey: settings.WorkerValidKey,
          WorkerAllowHttpImageRequestEnabled:
            settings.WorkerAllowHttpImageRequestEnabled,
        }}
      />
    ),
  },
  {
    id: 'ionet',
    titleKey: 'io.net Deployments',
    descriptionKey: 'Configure IoNet model deployment settings',
    build: (settings: IntegrationSettings) => (
      <IoNetDeploymentSettingsSection
        defaultValues={{
          enabled: settings['model_deployment.ionet.enabled'],
          apiKey: settings['model_deployment.ionet.api_key'],
        }}
      />
    ),
  },
  {
    id: 'monitoring',
    titleKey: 'Monitoring & Alerts',
    descriptionKey: 'Configure channel monitoring and automation',
    build: (settings: IntegrationSettings) => (
      <MonitoringSettingsSection
        defaultValues={{
          ChannelDisableThreshold: settings.ChannelDisableThreshold,
          QuotaRemindThreshold: settings.QuotaRemindThreshold,
          AutomaticDisableChannelEnabled:
            settings.AutomaticDisableChannelEnabled,
          AutomaticEnableChannelEnabled: settings.AutomaticEnableChannelEnabled,
          AutomaticDisableKeywords: settings.AutomaticDisableKeywords,
          AutomaticDisableStatusCodes: settings.AutomaticDisableStatusCodes,
          AutomaticRetryStatusCodes: settings.AutomaticRetryStatusCodes,
          'monitor_setting.auto_test_channel_enabled':
            settings['monitor_setting.auto_test_channel_enabled'],
          'monitor_setting.auto_test_channel_minutes':
            settings['monitor_setting.auto_test_channel_minutes'],
          'channel_health_setting.enabled':
            settings['channel_health_setting.enabled'],
          'channel_health_setting.base_degrade_threshold':
            settings['channel_health_setting.base_degrade_threshold'],
          'channel_health_setting.level_step_threshold':
            settings['channel_health_setting.level_step_threshold'],
          'channel_health_setting.max_degrade_level':
            settings['channel_health_setting.max_degrade_level'],
          'channel_health_setting.min_weight_factor':
            settings['channel_health_setting.min_weight_factor'],
          'channel_health_setting.max_ttft_ms':
            settings['channel_health_setting.max_ttft_ms'],
          'channel_health_setting.latency_degrade_base':
            settings['channel_health_setting.latency_degrade_base'],
          'channel_health_setting.latency_degrade_step':
            settings['channel_health_setting.latency_degrade_step'],
          'channel_health_setting.count_latency_as_error':
            settings['channel_health_setting.count_latency_as_error'],
          'channel_health_setting.disable_threshold':
            settings['channel_health_setting.disable_threshold'],
          'channel_health_setting.upgrade_threshold':
            settings['channel_health_setting.upgrade_threshold'],
          'channel_health_setting.degrade_probe_minutes':
            settings['channel_health_setting.degrade_probe_minutes'],
          'channel_health_setting.degrade_probe_count':
            settings['channel_health_setting.degrade_probe_count'],
          'channel_health_setting.count_429_as_error':
            settings['channel_health_setting.count_429_as_error'],
          'channel_health_setting.countable_status_codes':
            settings['channel_health_setting.countable_status_codes'],
          'channel_health_setting.notify_on_degrade':
            settings['channel_health_setting.notify_on_degrade'],
          'channel_health_setting.notify_on_upgrade':
            settings['channel_health_setting.notify_on_upgrade'],
          'channel_health_setting.streak_window_sec':
            settings['channel_health_setting.streak_window_sec'],
          'channel_health_setting.rebounce_protection_minutes':
            settings['channel_health_setting.rebounce_protection_minutes'],
          'channel_health_setting.rebounce_protection_threshold':
            settings['channel_health_setting.rebounce_protection_threshold'],
          'channel_health_setting.demote_cooldown_sec':
            settings['channel_health_setting.demote_cooldown_sec'],
          'channel_health_setting.recovery_strategy':
            settings['channel_health_setting.recovery_strategy'],
          'channel_health_setting.recovery_probe_minutes':
            settings['channel_health_setting.recovery_probe_minutes'],
          'channel_health_setting.recovery_probe_model':
            settings['channel_health_setting.recovery_probe_model'],
          'channel_health_setting.shadow_sample_rate':
            settings['channel_health_setting.shadow_sample_rate'],
          'channel_health_setting.skip_channel_tags':
            settings['channel_health_setting.skip_channel_tags'],
        }}
      />
    ),
  },
  {
    id: 'reconcile-bill',
    titleKey: 'Reconcile Upstream Bill',
    descriptionKey:
      'Pull upstream spend for the reconcile tab from the balance panel',
    build: (settings: IntegrationSettings) => (
      <ReconcileBillSettingsSection
        defaultValues={{
          ReconcileBalancePanelBaseURL:
            settings.ReconcileBalancePanelBaseURL ?? '',
          ReconcileBalancePanelToken: '',
        }}
      />
    ),
  },
  {
    id: 'sub-site-sync',
    titleKey: 'Sub-Site Sync',
    descriptionKey:
      'Configure upstream new-api sites used to sync groups & pricing.',
    build: (_settings: IntegrationSettings) => <SubSiteSettingsSection />,
  },
] as const

export type IntegrationSectionId = (typeof INTEGRATIONS_SECTIONS)[number]['id']

const integrationsRegistry = createSectionRegistry<
  IntegrationSectionId,
  IntegrationSettings
>({
  sections: INTEGRATIONS_SECTIONS,
  defaultSection: 'payment',
  basePath: '/system-settings/integrations',
  urlStyle: 'path',
})

export const INTEGRATIONS_SECTION_IDS = integrationsRegistry.sectionIds
export const INTEGRATIONS_DEFAULT_SECTION = integrationsRegistry.defaultSection
export const getIntegrationsSectionNavItems =
  integrationsRegistry.getSectionNavItems
export const getIntegrationsSectionContent =
  integrationsRegistry.getSectionContent
