import { Outlet } from '@tanstack/react-router'
import { SectionPageLayout } from '@/components/layout'

export function SystemSettings() {
  return (
    <SectionPageLayout>
      <SectionPageLayout.Content>
        <Outlet />
      </SectionPageLayout.Content>
    </SectionPageLayout>
  )
}
