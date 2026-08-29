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
      className={cn('bg-background z-50 h-16 shrink-0 border-b', className)}
      {...props}
    >
      <div className='flex h-full items-center gap-3 p-4 sm:gap-4'>
        <SidebarTrigger variant='outline' />
        {isAdmin && <PinnedUsersPopover />}
        <Separator orientation='vertical' className='h-6' />
        {children}
      </div>
    </header>
  )
}
