import {
  Children,
  isValidElement,
  useState,
  type ReactElement,
  type ReactNode,
} from 'react'
import { AppHeader } from './app-header'
import { Main } from './main'
import { PageFooterProvider } from './page-footer'

type SlotProps = { children?: ReactNode }

function SectionPageLayoutTitle(_props: SlotProps) {
  return null
}
SectionPageLayoutTitle.displayName = 'SectionPageLayout.Title'

function SectionPageLayoutDescription(_props: SlotProps) {
  return null
}
SectionPageLayoutDescription.displayName = 'SectionPageLayout.Description'

function SectionPageLayoutActions(_props: SlotProps) {
  return null
}
SectionPageLayoutActions.displayName = 'SectionPageLayout.Actions'

function SectionPageLayoutContent(_props: SlotProps) {
  return null
}
SectionPageLayoutContent.displayName = 'SectionPageLayout.Content'

function SectionPageLayoutBreadcrumb(_props: SlotProps) {
  return null
}
SectionPageLayoutBreadcrumb.displayName = 'SectionPageLayout.Breadcrumb'

export type SectionPageLayoutProps = {
  children: ReactNode
}

/** 40px 紫色网格，随内容一起滚动（bg-local），见 STYLE.md */
const PAGE_GRID =
  'bg-[linear-gradient(to_right,rgba(99,91,255,0.1)_1px,transparent_1px),linear-gradient(to_bottom,rgba(99,91,255,0.1)_1px,transparent_1px)] bg-[size:40px_40px] bg-local dark:bg-[linear-gradient(to_right,rgba(124,116,255,0.16)_1px,transparent_1px),linear-gradient(to_bottom,rgba(124,116,255,0.16)_1px,transparent_1px)]'

/** 不限宽：宽屏多出的空间按栅格比例分给各列，不在两侧留白 */
const PAGE_SHELL = 'px-6 md:px-10'

export function SectionPageLayout(props: SectionPageLayoutProps) {
  const [footerContainer, setFooterContainer] = useState<HTMLDivElement | null>(
    null
  )

  let title: ReactNode = null
  let description: ReactNode = null
  let actions: ReactNode = null
  let content: ReactNode = null
  let breadcrumb: ReactNode = null

  Children.forEach(props.children, (node) => {
    if (!isValidElement(node)) return
    const child = node as ReactElement<SlotProps>
    if (child.type === SectionPageLayoutTitle) title = child.props.children
    else if (child.type === SectionPageLayoutDescription)
      description = child.props.children
    else if (child.type === SectionPageLayoutActions)
      actions = child.props.children
    else if (child.type === SectionPageLayoutContent)
      content = child.props.children
    else if (child.type === SectionPageLayoutBreadcrumb)
      breadcrumb = child.props.children
  })

  // 个人资料、系统设置这类页面没有页头槽位：不渲染空页头，内容区自己补顶部间距
  const hasHeader =
    title != null ||
    description != null ||
    actions != null ||
    breadcrumb != null

  return (
    <PageFooterProvider container={footerContainer}>
      <AppHeader />

      <Main className='bg-background'>
        {/* 页头和内容共用一个滚动容器：整页一起滚，卡片不会被固定页头切断；
            始终预留滚动条槽位，与分页栏对齐，长短页面切换时内容不左右跳；
            relative 让内部绝对定位元素（如 sr-only）以它为包含块，否则会逃出裁剪、撑出整页第二条滚动条 */}
        <div
          className={`relative min-h-0 flex-1 overflow-auto [scrollbar-gutter:stable] ${PAGE_GRID}`}
        >
          {hasHeader && (
            <div className={`${PAGE_SHELL} pt-8`}>
              {breadcrumb != null && (
                <div className='mb-2 sm:mb-3'>{breadcrumb}</div>
              )}
              <div className='flex flex-wrap items-end justify-between gap-4'>
                <div className='min-w-0'>
                  <h2 className='text-foreground text-3xl font-semibold md:text-4xl'>
                    {title}
                  </h2>
                  {description != null && (
                    <p className='text-muted-foreground mt-2 max-w-prose text-base text-pretty'>
                      {description}
                    </p>
                  )}
                </div>
                {actions != null && (
                  <div className='flex max-w-full shrink-0 flex-wrap items-center gap-2 sm:gap-x-4'>
                    {actions}
                  </div>
                )}
              </div>
            </div>
          )}
          <div className={`${PAGE_SHELL} ${hasHeader ? 'pt-6' : 'pt-8'} pb-10`}>
            {content}
          </div>
        </div>

        {/* 分页栏底色与上边框保持全宽，内容挂进与正文相同的外壳容器，左缘对齐 */}
        <div className='bg-background shrink-0 overflow-hidden border-t py-2.5 [scrollbar-gutter:stable] has-[>div:empty]:hidden sm:py-3'>
          <div ref={setFooterContainer} className={PAGE_SHELL} />
        </div>
      </Main>
    </PageFooterProvider>
  )
}

SectionPageLayout.Title = SectionPageLayoutTitle
SectionPageLayout.Description = SectionPageLayoutDescription
SectionPageLayout.Actions = SectionPageLayoutActions
SectionPageLayout.Content = SectionPageLayoutContent
SectionPageLayout.Breadcrumb = SectionPageLayoutBreadcrumb
