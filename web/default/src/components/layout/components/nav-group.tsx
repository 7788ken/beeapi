import { type ReactNode, useState, useEffect } from 'react'
import { Link, useLocation } from '@tanstack/react-router'
import { ChevronDown, ChevronRight } from 'lucide-react'
import { Badge } from '@/components/ui/badge'
import {
  Collapsible,
  CollapsibleContent,
  CollapsibleTrigger,
} from '@/components/ui/collapsible'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import {
  SidebarGroup,
  SidebarGroupLabel,
  SidebarMenu,
  SidebarMenuButton,
  SidebarMenuItem,
  SidebarMenuSub,
  SidebarMenuSubButton,
  SidebarMenuSubItem,
  useSidebar,
} from '@/components/ui/sidebar'
import { checkIsActive } from '../lib/url-utils'
import {
  type NavCollapsible,
  type NavChatPresets,
  type NavLink,
  type NavGroup as NavGroupProps,
} from '../types'
import { ChatPresetsItem } from './chat-presets-item'

/**
 * Sidebar navigation group component
 * Renders a collapsible group of navigation items
 */
export function NavGroup({ title, items, id }: NavGroupProps) {
  const { state, isMobile } = useSidebar()
  const href = useLocation({ select: (location) => location.href })
  const storageKey = `sidebar-group-${id || title}`
  const [isOpen, setIsOpen] = useState(() => {
    const saved = localStorage.getItem(storageKey)
    return saved === null ? true : saved === '1'
  })

  const handleOpenChange = (open: boolean) => {
    setIsOpen(open)
    localStorage.setItem(storageKey, open ? '1' : '0')
  }

  return (
    <Collapsible open={isOpen} onOpenChange={handleOpenChange} className='group/nav-group'>
      <SidebarGroup>
        <CollapsibleTrigger asChild>
          <SidebarGroupLabel className='cursor-pointer select-none hover:text-sidebar-foreground'>
            <span className='flex-1'>{title}</span>
            <ChevronDown className='h-3 w-3 transition-transform duration-200 group-data-[state=closed]/nav-group:-rotate-90' />
          </SidebarGroupLabel>
        </CollapsibleTrigger>
        <CollapsibleContent>
          <SidebarMenu>
            {items.map((item) => {
              const key = `${item.title}-${item.url || item.type}`

              if (item.type === 'chat-presets') {
                return <ChatPresetsItem key={key} item={item as NavChatPresets} />
              }

              if (!item.items) {
                return (
                  <SidebarMenuLink key={key} item={item as NavLink} href={href} />
                )
              }

              if (state === 'collapsed' && !isMobile) {
                return (
                  <SidebarMenuCollapsedDropdown
                    key={key}
                    item={item as NavCollapsible}
                    href={href}
                  />
                )
              }

              return (
                <SidebarMenuCollapsible
                  key={key}
                  item={item as NavCollapsible}
                  href={href}
                />
              )
            })}
          </SidebarMenu>
        </CollapsibleContent>
      </SidebarGroup>
    </Collapsible>
  )
}

/**
 * Navigation badge component
 */
function NavBadge({ children }: { children: ReactNode }) {
  return <Badge className='rounded-full px-1 py-0 text-xs'>{children}</Badge>
}

/**
 * Sidebar menu link item
 */
function SidebarMenuLink({ item, href }: { item: NavLink; href: string }) {
  const { setOpenMobile } = useSidebar()
  return (
    <SidebarMenuItem>
      <SidebarMenuButton
        asChild
        isActive={item.external ? false : checkIsActive(href, item)}
        tooltip={item.title}
      >
        {item.external ? (
          <a
            href={item.url as string}
            target='_blank'
            rel='noopener noreferrer'
            onClick={() => setOpenMobile(false)}
          >
            {item.icon && <item.icon className={item.iconClassName} />}
            <span>{item.title}</span>
            {item.badge && <NavBadge>{item.badge}</NavBadge>}
          </a>
        ) : (
          <Link to={item.url} onClick={() => setOpenMobile(false)}>
            {item.icon && <item.icon className={item.iconClassName} />}
            <span>{item.title}</span>
            {item.badge && <NavBadge>{item.badge}</NavBadge>}
          </Link>
        )}
      </SidebarMenuButton>
    </SidebarMenuItem>
  )
}

/**
 * Sidebar collapsible menu item
 */
function SidebarMenuCollapsible({
  item,
  href,
}: {
  item: NavCollapsible
  href: string
}) {
  const { setOpenMobile } = useSidebar()
  // 检查当前路径是否匹配子菜单项
  const isSubItemActive = checkIsActive(href, item)
  // 使用受控状态，初始值基于当前路径是否匹配
  const [isOpen, setIsOpen] = useState(() => isSubItemActive)

  // 当路径变化时，如果匹配子菜单项，自动展开父级菜单
  useEffect(() => {
    if (isSubItemActive) {
      // eslint-disable-next-line react-hooks/set-state-in-effect
      setIsOpen(true)
    }
  }, [isSubItemActive])

  return (
    <Collapsible
      asChild
      open={isOpen}
      onOpenChange={setIsOpen}
      className='group/collapsible'
    >
      <SidebarMenuItem>
        <CollapsibleTrigger asChild>
          <SidebarMenuButton tooltip={item.title}>
            {item.icon && <item.icon className={item.iconClassName} />}
            <span>{item.title}</span>
            {item.badge && <NavBadge>{item.badge}</NavBadge>}
            <ChevronRight className='ms-auto transition-transform duration-200 group-data-[state=open]/collapsible:rotate-90' />
          </SidebarMenuButton>
        </CollapsibleTrigger>
        <CollapsibleContent className='CollapsibleContent'>
          <SidebarMenuSub>
            {item.items.map((subItem) => (
              <SidebarMenuSubItem key={subItem.title}>
                <SidebarMenuSubButton
                  asChild
                  isActive={checkIsActive(href, subItem)}
                >
                  <Link to={subItem.url} onClick={() => setOpenMobile(false)}>
                    {subItem.icon && <subItem.icon />}
                    <span>{subItem.title}</span>
                    {subItem.badge && <NavBadge>{subItem.badge}</NavBadge>}
                  </Link>
                </SidebarMenuSubButton>
              </SidebarMenuSubItem>
            ))}
          </SidebarMenuSub>
        </CollapsibleContent>
      </SidebarMenuItem>
    </Collapsible>
  )
}

/**
 * Sidebar dropdown menu item when collapsed
 */
function SidebarMenuCollapsedDropdown({
  item,
  href,
}: {
  item: NavCollapsible
  href: string
}) {
  return (
    <SidebarMenuItem>
      <DropdownMenu>
        <DropdownMenuTrigger asChild>
          <SidebarMenuButton
            tooltip={item.title}
            isActive={checkIsActive(href, item)}
          >
            {item.icon && <item.icon className={item.iconClassName} />}
            <span>{item.title}</span>
            {item.badge && <NavBadge>{item.badge}</NavBadge>}
            <ChevronRight className='ms-auto transition-transform duration-200 group-data-[state=open]/collapsible:rotate-90' />
          </SidebarMenuButton>
        </DropdownMenuTrigger>
        <DropdownMenuContent side='right' align='start' sideOffset={4}>
          <DropdownMenuLabel>
            {item.title} {item.badge ? `(${item.badge})` : ''}
          </DropdownMenuLabel>
          <DropdownMenuSeparator />
          {item.items.map((sub) => (
            <DropdownMenuItem key={`${sub.title}-${sub.url}`} asChild>
              <Link
                to={sub.url}
                className={`${checkIsActive(href, sub) ? 'bg-secondary' : ''}`}
              >
                {sub.icon && <sub.icon />}
                <span className='max-w-52 text-wrap'>{sub.title}</span>
                {sub.badge && (
                  <span className='ms-auto text-xs'>{sub.badge}</span>
                )}
              </Link>
            </DropdownMenuItem>
          ))}
        </DropdownMenuContent>
      </DropdownMenu>
    </SidebarMenuItem>
  )
}
