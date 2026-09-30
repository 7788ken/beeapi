import { useMemo } from 'react'
import { Link } from '@tanstack/react-router'
import { Menu } from 'lucide-react'
import { cn } from '@/lib/utils'
import { Button } from '@/components/ui/button'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import { type TopNavLink } from '../types'

type TopNavProps = React.HTMLAttributes<HTMLElement> & {
  links: TopNavLink[]
}

/**
 * 顶部导航栏组件
 * 在大屏幕显示水平导航，在小屏幕显示下拉菜单
 */
export function TopNav({ className, links, ...props }: TopNavProps) {
  // 规范化链接，确保所有可选属性都有默认值
  const normalizedLinks = useMemo(
    () =>
      links.map((link) => ({
        isActive: false,
        disabled: false,
        external: false,
        ...link,
      })),
    [links]
  )

  return (
    <>
      {/* 移动端下拉菜单 */}
      <div className='lg:hidden'>
        <DropdownMenu modal={false}>
          <DropdownMenuTrigger asChild>
            <Button
              size='icon'
              variant='outline'
              className='border-border bg-card text-foreground size-9 rounded-lg shadow-[0_1px_2px_rgba(0,0,0,0.05),inset_0_1px_0_rgba(255,255,255,0.9)] transition-all duration-[300ms] ease-out hover:-translate-y-0.5 active:translate-y-0 active:scale-[0.98] motion-reduce:transition-none motion-reduce:hover:translate-y-0 motion-reduce:active:scale-100 dark:shadow-[0_1px_2px_rgba(0,0,0,0.4),inset_0_1px_0_rgba(255,255,255,0.08)]'
            >
              <Menu />
            </Button>
          </DropdownMenuTrigger>
          <DropdownMenuContent side='bottom' align='start'>
            {normalizedLinks.map(
              ({ title, href, isActive, disabled, external }) => (
                <DropdownMenuItem key={`${title}-${href}`} asChild>
                  {external ? (
                    <a
                      href={href}
                      target='_blank'
                      rel='noopener noreferrer'
                      className={
                        isActive ? 'text-foreground' : 'text-muted-foreground'
                      }
                    >
                      {title}
                    </a>
                  ) : (
                    <Link
                      to={href}
                      className={
                        isActive ? 'text-foreground' : 'text-muted-foreground'
                      }
                      disabled={disabled}
                    >
                      {title}
                    </Link>
                  )}
                </DropdownMenuItem>
              )
            )}
          </DropdownMenuContent>
        </DropdownMenu>
      </div>

      {/* 桌面端水平导航 */}
      <nav
        className={cn(
          'hidden items-center space-x-4 lg:flex lg:space-x-4 xl:space-x-6',
          className
        )}
        {...props}
      >
        {normalizedLinks.map(({ title, href, isActive, disabled, external }) =>
          external ? (
            <a
              key={`${title}-${href}`}
              href={href}
              target='_blank'
              rel='noopener noreferrer'
              className={`text-sm font-medium transition-colors duration-[300ms] ease-out hover:text-[#635bff] ${isActive ? 'text-foreground' : 'text-muted-foreground'}`}
            >
              {title}
            </a>
          ) : (
            <Link
              key={`${title}-${href}`}
              to={href}
              disabled={disabled}
              className={`text-sm font-medium transition-colors duration-[300ms] ease-out hover:text-[#635bff] ${isActive ? 'text-foreground' : 'text-muted-foreground'}`}
            >
              {title}
            </Link>
          )
        )}
      </nav>
    </>
  )
}
