import { getCookie } from '@/lib/cookies'
import { cn } from '@/lib/utils'
import { LayoutProvider } from '@/context/layout-provider'
import { SearchProvider } from '@/context/search-provider'
import { SidebarInset, SidebarProvider } from '@/components/ui/sidebar'
import { AnimatedOutlet } from '@/components/page-transition'
import { SkipToMain } from '@/components/skip-to-main'
import { WorkspaceProvider } from '../context/workspace-context'
import { AppSidebar } from './app-sidebar'

type AuthenticatedLayoutProps = {
  children?: React.ReactNode
}

export function AuthenticatedLayout(props: AuthenticatedLayoutProps) {
  const defaultOpen = getCookie('sidebar_state') !== 'false'

  return (
    <LayoutProvider>
      <SearchProvider>
        <WorkspaceProvider>
          <SidebarProvider defaultOpen={defaultOpen}>
            <SkipToMain />
            <AppSidebar />
            <SidebarInset
              className={cn(
                '@container/content',
                'h-svh',
                'bg-[#f6f9fc] bg-[linear-gradient(to_right,rgba(99,91,255,0.1)_1px,transparent_1px),linear-gradient(to_bottom,rgba(99,91,255,0.1)_1px,transparent_1px)] bg-[size:40px_40px] dark:bg-[#0a2540] dark:bg-[linear-gradient(to_right,rgba(124,116,255,0.16)_1px,transparent_1px),linear-gradient(to_bottom,rgba(124,116,255,0.16)_1px,transparent_1px)]',
                'peer-data-[variant=inset]:h-[calc(100svh-(var(--spacing)*4))]'
              )}
            >
              {props.children ?? <AnimatedOutlet />}
            </SidebarInset>
          </SidebarProvider>
        </WorkspaceProvider>
      </SearchProvider>
    </LayoutProvider>
  )
}
