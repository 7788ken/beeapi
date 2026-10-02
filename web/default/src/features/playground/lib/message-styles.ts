// 对话气泡：我 = 主色实心白字、靠右；模型 = 白卡片、靠左。宽度按容器百分比自适应，不再固定 768px 居中列。
// STYLE.md 第二章：紫底上只放白字，所以用户气泡里的链接、行内代码、引用、表格线都换成白色系。

/** 消息流与输入卡共用的列：铺满可用宽度，两侧边距随窗口在 16–48px 之间缩放（宽屏不留两侧空白） */
export const chatColumn = 'w-full px-[clamp(1rem,3vw,3rem)]'

// Streamdown 根节点自带 whitespace-normal，段落里的单个换行要在 p 上恢复；
// 气泡里正文 14px，标题降一档；引用块左边线改细（STYLE.md：不用单侧粗边框）
const bubbleBase = [
  'w-fit min-w-0 rounded-xl px-4 py-2.5 text-sm leading-7 break-words [overflow-wrap:anywhere]',
  '[&_p]:whitespace-pre-wrap [&_pre]:max-w-full',
  '[&_h1]:text-lg [&_h2]:text-base [&_h3]:text-sm [&_h1]:mt-4 [&_h2]:mt-4 [&_h3]:mt-3',
  '[&_blockquote]:border-l-2',
].join(' ')

/** 我的消息：主色实心、白字；Streamdown 的链接是按钮（点击先确认再打开） */
export const userBubble = [
  bubbleBase,
  // 宽度比例参考 ChatGPT（Webby 2023 年度突破奖）：用户气泡最宽约七成
  'bg-primary text-primary-foreground max-w-[85%] sm:max-w-[70%]',
  'shadow-[0_1px_2px_rgba(10,37,64,0.12),inset_0_1px_0_rgba(255,255,255,0.15)]',
  '[&_[data-streamdown=link]]:text-primary-foreground [&_[data-streamdown=link]]:underline-offset-2',
  // 行内代码用压暗底：白字对比度 7.2:1；叠半透明白会掉到 3.6:1
  '[&_:not(pre)>code]:bg-[rgba(10,37,64,0.35)] [&_:not(pre)>code]:text-primary-foreground',
  '[&_blockquote]:border-white/40 [&_blockquote]:text-primary-foreground',
  '[&_hr]:border-white/30 [&_td]:border-white/30 [&_th]:border-white/30',
].join(' ')

export const assistantBubble = [
  bubbleBase,
  'bg-card text-card-foreground shadow-surface max-w-full border sm:max-w-[88%]',
  // 暗色卡片 #10345a 上主色紫链接对比度约 2.5:1，换浅一档（约 5.5:1）
  'dark:[&_[data-streamdown=link]]:text-[#a8a3ff]',
].join(' ')
