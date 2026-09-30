import { ContentBackupSettingsForm } from '@/features/content-backup/components/settings-form'
import type { IntegrationSettings } from '../types'
import { createSectionRegistry } from '../utils/section-registry'
import { AgouSettingsSection } from './agou-settings-section'
import { EmailSettingsSection } from './email-settings-section'
import { IoNetDeploymentSettingsSection } from './ionet-deployment-settings-section'
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
  {
    id: 'content-backup',
    titleKey: 'Content backup',
    descriptionKey:
      'Configure the remote upload target (FTPS or SFTP), capture limits and operational tuning for the content backup pipeline.',
    build: (_settings: IntegrationSettings) => <ContentBackupSettingsForm />,
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
