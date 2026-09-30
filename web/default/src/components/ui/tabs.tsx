import * as React from 'react'
import * as TabsPrimitive from '@radix-ui/react-tabs'
import { cn } from '@/lib/utils'

function Tabs({
  className,
  ...props
}: React.ComponentProps<typeof TabsPrimitive.Root>) {
  return (
    <TabsPrimitive.Root
      data-slot='tabs'
      className={cn('flex flex-col gap-2', className)}
      {...props}
    />
  )
}

function TabsList({
  className,
  ...props
}: React.ComponentProps<typeof TabsPrimitive.List>) {
  return (
    <TabsPrimitive.List
      data-slot='tabs-list'
      className={cn(
        'bg-card text-muted-foreground inline-flex h-9 w-fit items-center justify-center gap-0.5 rounded-lg border p-0.5',
        className
      )}
      {...props}
    />
  )
}

function TabsTrigger({
  className,
  ...props
}: React.ComponentProps<typeof TabsPrimitive.Trigger>) {
  return (
    <TabsPrimitive.Trigger
      data-slot='tabs-trigger'
      className={cn(
        // 激活项实心主色白字，见 STYLE.md「层级与强调」；原来的 bg-background 叠在 bg-muted 轨道上看不出选中。
        // group/tab 供子元素写 group-data-[state=active]/tab:*，让徽标、状态点在紫底上换成白色系。
        // 高度用 self-stretch 跟随所在行：h-full 在"定高纵向 flex 里的换行列表"会按整个列表高度撑开每个标签
        "group/tab text-muted-foreground data-[state=inactive]:hover:bg-foreground/5 data-[state=inactive]:hover:text-foreground data-[state=active]:bg-primary data-[state=active]:text-primary-foreground focus-visible:border-ring focus-visible:ring-ring/50 focus-visible:outline-ring inline-flex flex-1 items-center justify-center gap-1.5 self-stretch rounded-md border border-transparent px-3 py-1 text-sm font-medium whitespace-nowrap transition-all duration-[300ms] ease-out focus-visible:ring-[3px] focus-visible:outline-1 active:scale-[0.98] disabled:pointer-events-none disabled:opacity-50 data-[state=active]:shadow-[0_2px_5px_rgba(99,91,255,0.4),inset_0_1px_0_rgba(255,255,255,0.2)] motion-reduce:transition-none motion-reduce:active:scale-100 [&_svg]:pointer-events-none [&_svg]:shrink-0 [&_svg:not([class*='size-'])]:size-4",
        className
      )}
      {...props}
    />
  )
}

function TabsContent({
  className,
  ...props
}: React.ComponentProps<typeof TabsPrimitive.Content>) {
  return (
    <TabsPrimitive.Content
      data-slot='tabs-content'
      className={cn('flex-1 outline-none', className)}
      {...props}
    />
  )
}

export { Tabs, TabsList, TabsTrigger, TabsContent }
