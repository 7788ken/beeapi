import {
  LayoutDashboard,
  LayoutGrid,
  Activity,
  Key,
  FileText,
  Wallet,
  Box,
  Users,
  Ticket,
  User,
  Command,
  Radio,
  FlaskConical,
  MessageSquare,
  CreditCard,
  Crown,
  ListTodo,
  Settings,
  Share2,
  ShieldAlert,
  AlertTriangle,
  Sparkles,
} from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { WORKSPACE_IDS } from '@/components/layout/lib/workspace-registry'
import { type NavItem, type SidebarData } from '@/components/layout/types'
import { useStatus } from '@/hooks/use-status'

export function useSidebarData(): SidebarData {
  const { t } = useTranslation()
  const { status } = useStatus()
  const topUpLink =
    typeof status?.top_up_link === 'string' ? status.top_up_link.trim() : ''

  const personalItems: NavItem[] = [
    {
      title: t('Wallet'),
      url: '/wallet',
      icon: Wallet,
      iconClassName: 'text-amber-500',
    },
    {
      title: t('Referral Program'),
      url: '/referral',
      icon: Share2,
      iconClassName: 'text-pink-500',
    },
    {
      title: t('Subscription Plans'),
      url: '/my-subscription',
      icon: Crown,
      iconClassName: 'text-yellow-500',
    },
    {
      title: t('Profile'),
      url: '/profile',
      icon: User,
      iconClassName: 'text-slate-500',
    },
  ]
  if (topUpLink) {
    personalItems.push({
      title: t('Card Top-Up'),
      url: topUpLink,
      icon: CreditCard,
      iconClassName: 'text-emerald-500',
      external: true,
    })
  }

  return {
    workspaces: [
      {
        id: WORKSPACE_IDS.DEFAULT,
        name: '', // Dynamically fetches system name
        logo: Command,
        plan: '', // Dynamically fetches system version
      },
    ],
    navGroups: [
      {
        id: 'chat',
        title: t('Chat'),
        items: [
          {
            title: t('Create Center'),
            url: '/create-center',
            icon: Sparkles,
            iconClassName: 'text-pink-500',
          },
          {
            title: t('Playground'),
            url: '/playground',
            icon: FlaskConical,
            iconClassName: 'text-violet-500',
          },
          {
            title: t('Chat'),
            icon: MessageSquare,
            iconClassName: 'text-blue-500',
            type: 'chat-presets',
          },
        ],
      },
      {
        id: 'general',
        title: t('General'),
        items: [
          {
            title: t('Overview'),
            url: '/dashboard/overview',
            icon: Activity,
            iconClassName: 'text-green-500',
          },
          {
            title: t('Dashboard'),
            url: '/dashboard/models',
            icon: LayoutDashboard,
            iconClassName: 'text-blue-500',
          },
          {
            title: t('API Keys'),
            url: '/keys',
            icon: Key,
            iconClassName: 'text-orange-500',
          },
          {
            title: t('Group Square'),
            url: '/group-square',
            icon: LayoutGrid,
            iconClassName: 'text-indigo-500',
          },
          {
            title: t('Usage Logs'),
            url: '/usage-logs/common',
            icon: FileText,
            iconClassName: 'text-gray-500',
          },
          {
            title: t('Task Logs'),
            url: '/usage-logs/task',
            activeUrls: ['/usage-logs/drawing'],
            configUrls: ['/usage-logs/drawing', '/usage-logs/task'],
            icon: ListTodo,
            iconClassName: 'text-teal-500',
          },
        ],
      },
      {
        id: 'personal',
        title: t('Personal'),
        items: personalItems,
      },
      {
        id: 'admin',
        title: t('Admin'),
        items: [
          {
            title: t('Channels'),
            url: '/channels',
            icon: Radio,
            iconClassName: 'text-rose-500',
          },
          {
            title: t('Models'),
            url: '/models/metadata',
            icon: Box,
            iconClassName: 'text-purple-500',
          },
          {
            title: t('Users'),
            url: '/users',
            icon: Users,
            iconClassName: 'text-sky-500',
          },
          {
            title: t('Redemption Codes'),
            url: '/redemption-codes',
            icon: Ticket,
            iconClassName: 'text-lime-500',
          },
          {
            title: t('Subscription Management'),
            url: '/subscriptions',
            icon: CreditCard,
            iconClassName: 'text-emerald-500',
          },
          {
            title: t('Sensitive Monitor'),
            url: '/sensitive-monitor',
            icon: ShieldAlert,
            iconClassName: 'text-red-500',
          },
          {
            title: t('Anomaly Monitor'),
            url: '/anomaly-monitor',
            icon: AlertTriangle,
            iconClassName: 'text-amber-500',
          },
          {
            title: t('System Settings'),
            url: '/system-settings/general',
            activeUrls: ['/system-settings'],
            icon: Settings,
            iconClassName: 'text-zinc-500',
          },
        ],
      },
    ],
  }
}
