import { useAuthStore } from '@/stores/auth-store'
import { ROLE } from '@/lib/roles'
import { cn } from '@/lib/utils'
import { useAdminPerms } from '@/hooks/use-admin'
import { Separator } from '@/components/ui/separator'
import { SidebarTrigger } from '@/components/ui/sidebar'
import { PinnedUsersPopover } from './pinned-users-popover'

type HeaderProps = React.HTMLAttributes<HTMLElement>

export function Header({ className, children, ...props }: HeaderProps) {
  const userRole = useAuthStore((state) => state.auth.user?.role)
  const perms = useAdminPerms()
  // 置顶用户读的是 /api/user/pinned，管理员没有用户列表权限时那个接口会 403
  const isAdmin =
    userRole != null &&
    userRole >= ROLE.ADMIN &&
    (perms.user_manage || perms.quota_grant)

  return (
    <header
      className={cn(
        'z-50 h-16 shrink-0 border-b border-gray-200 bg-white text-[#0a2540] shadow-[0_1px_2px_rgba(0,0,0,0.05)] dark:border-white/10 dark:bg-[#06182c] dark:text-[#f6f9fc]',
        className
      )}
      {...props}
    >
      <div className='flex h-full items-center gap-3 px-6 md:px-8'>
        <SidebarTrigger
          variant='outline'
          className='border-border bg-card text-foreground size-9 rounded-lg border shadow-[0_1px_2px_rgba(0,0,0,0.05),inset_0_1px_0_rgba(255,255,255,0.9)] transition-all duration-[300ms] ease-out hover:-translate-y-0.5 active:translate-y-0 active:scale-[0.98] active:shadow-[inset_0_2px_4px_rgba(0,0,0,0.2)] motion-reduce:transition-none motion-reduce:hover:translate-y-0 motion-reduce:active:scale-100 dark:shadow-[0_1px_2px_rgba(0,0,0,0.4),inset_0_1px_0_rgba(255,255,255,0.08)]'
        />
        {isAdmin && <PinnedUsersPopover />}
        <Separator orientation='vertical' className='h-6' />
        {children}
      </div>
    </header>
  )
}
