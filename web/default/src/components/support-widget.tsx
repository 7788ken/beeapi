import { useEffect } from 'react'
import { useTranslation } from 'react-i18next'
import { useStatus } from '@/hooks/use-status'

const NINE_DESK_ORIGIN = 'https://desk.9manager.com'

// 9desk 启动时把主题属性值原样写进它 shadow root 的 :host。值用 var() 引用本站令牌，
// 在宿主节点上解析，所以深浅色切换时客服窗口跟着变，不用重新加载
const NINE_DESK_THEME: Record<string, string> = {
  'data-accent-color': 'var(--primary)',
  'data-accent-hover-color':
    'color-mix(in oklab, var(--primary) 90%, transparent)',
  'data-panel-bg': 'var(--background)',
  'data-surface-color': 'var(--card)',
  'data-text-color': 'var(--foreground)',
  'data-text-muted-color': 'var(--muted-foreground)',
  'data-border-color': 'var(--border)',
  'data-panel-radius': 'calc(var(--radius) + 4px)',
}

// 主题属性管不到的写死颜色（深色下刺眼的浅灰描边和占位符、红色提示条），按本站的角标、焦点环、
// 链接和滚动条样式改写。有 9desk 令牌的用令牌，管理员改了主题属性这里也跟着变
const NINE_DESK_CSS = `
.bubble { box-shadow: 0 6px 20px color-mix(in oklab, var(--9d-accent) 40%, transparent), inset 0 1px 0 rgba(255,255,255,.2); }
.badge { background: #fff; color: var(--9d-accent); font-weight: 600; }
.contact-toggle, .contact-caret, .attach { color: var(--9d-text-muted); }
.contact-kind, .contact-value, .composer textarea, .attach { border-color: var(--input); }
.contact-value { background: var(--9d-surface); color: var(--9d-text); }
.contact-value::placeholder, .composer textarea::placeholder { color: var(--9d-text-muted); }
.contact-kind:focus, .contact-value:focus, .composer textarea:focus {
  outline: none; border-color: var(--9d-accent);
  box-shadow: 0 0 0 3px color-mix(in oklab, var(--9d-accent) 50%, transparent);
}
.messages { scrollbar-width: thin; }
.bubble-text.md a { color: inherit; font-weight: 500; }
.bubble-text.md blockquote { border-left-color: var(--9d-border); color: var(--9d-text-muted); }
.bubble-text.is-retracted { border-color: var(--9d-border); }
.bubble-text.md code { background: var(--muted); }
.notice { background: var(--muted); color: var(--9d-text); font-weight: 500; }
`

let injected = false

function isNineDesk(script: HTMLScriptElement) {
  if (script.src === '') return false
  const url = new URL(script.src)
  return url.origin === NINE_DESK_ORIGIN && url.pathname === '/widget.js'
}

// 嵌入代码里写了的属性以管理员为准；data-css 接在本站样式之后，同样是管理员的优先
function applyNineDeskDefaults(script: HTMLScriptElement, lang: string) {
  const defaults: Record<string, string> = {
    ...NINE_DESK_THEME,
    'data-font-family': getComputedStyle(document.body).fontFamily,
    'data-lang': lang,
  }
  for (const [name, value] of Object.entries(defaults)) {
    if (!script.hasAttribute(name)) script.setAttribute(name, value)
  }
  script.setAttribute(
    'data-css',
    NINE_DESK_CSS + (script.getAttribute('data-css') ?? '')
  )
}

// 按页面解析的顺序插入：非 async/defer 的外链脚本执行完才插下一个节点，
// 「先引 SDK 再初始化」这类嵌入代码才能照常工作
async function injectEmbedCode(code: string, lang: string) {
  const range = document.createRange()
  range.selectNodeContents(document.body)
  // createContextualFragment 解析出的脚本插入文档后会执行（innerHTML 的不会）
  const fragment = range.createContextualFragment(code)
  for (const node of Array.from(fragment.childNodes)) {
    if (!(node instanceof HTMLScriptElement)) {
      document.body.appendChild(node)
      continue
    }
    if (isNineDesk(node)) applyNineDeskDefaults(node, lang)
    // 看属性不看 node.async：动态插入的脚本 async 恒为 true
    if (
      node.src === '' ||
      node.hasAttribute('async') ||
      node.hasAttribute('defer')
    ) {
      document.body.appendChild(node)
      continue
    }
    await new Promise((resolve) => {
      node.addEventListener('load', resolve, { once: true })
      node.addEventListener('error', resolve, { once: true })
      document.body.appendChild(node)
    })
  }
}

/**
 * 在线客服：后台「内容设置 → 在线客服」填的嵌入代码原样注入 body，每次页面加载只注入一次，
 * 改了代码刷新页面生效（客服脚本一般没法卸载）。
 */
export function SupportWidget() {
  const { status } = useStatus()
  const { i18n } = useTranslation()
  const code = status?.['support_widget_code']
  const lang = i18n.resolvedLanguage ?? i18n.language

  useEffect(() => {
    if (typeof code !== 'string' || code === '' || injected) return
    injected = true
    void injectEmbedCode(code, lang)
  }, [code, lang])

  return null
}
