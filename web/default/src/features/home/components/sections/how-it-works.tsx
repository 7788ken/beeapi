import { Fragment, useEffect, useState } from 'react'
import { Check, Copy } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { cn } from '@/lib/utils'
import { useSystemConfig } from '@/hooks/use-system-config'
import { AnimateInView } from '@/components/animate-in-view'
import { Eyebrow, SectionHeading, homeAccent, homeMuted } from '../home-links'

type Sample = 'python' | 'curl' | 'node'

const TABS: { id: Sample; label: string }[] = [
  { id: 'python', label: 'Python' },
  { id: 'curl', label: 'cURL' },
  { id: 'node', label: 'Node.js' },
]

export function HowItWorks() {
  const { t } = useTranslation()
  const { systemName } = useSystemConfig()
  const [sample, setSample] = useState<Sample>('python')
  const [copied, setCopied] = useState(false)
  const origin =
    typeof window === 'undefined'
      ? 'https://your-gateway'
      : window.location.origin

  useEffect(() => {
    if (!copied) return
    const timer = setTimeout(() => setCopied(false), 1800)
    return () => clearTimeout(timer)
  }, [copied])

  const baseUrl = `${origin}/v1`

  const steps = [
    {
      num: '01',
      title: t('Create a key'),
      desc: t(
        'After you sign in, create an API key in the console. Keys can be limited by model and quota.'
      ),
    },
    {
      num: '02',
      title: t('Point the client here'),
      desc: t(
        'Replace the base URL in the client you already use. The request shape stays the same.'
      ),
      chip: baseUrl,
    },
    {
      num: '03',
      title: t('Send a request'),
      desc: t(
        'Call a model through the OpenAI-compatible route. Usage shows up in the console.'
      ),
    },
  ]

  const snippets: Record<Sample, string> = {
    python: `from openai import OpenAI

client = OpenAI(
    api_key="sk-••••",
    base_url="${baseUrl}",
)

resp = client.chat.completions.create(
    model="claude-sonnet-5",
    messages=[{"role": "user",
               "content": "用一句话介绍 ${systemName}"}],
    stream=True,
)`,
    curl: `curl ${baseUrl}/chat/completions \\
  -H "Authorization: Bearer sk-••••" \\
  -H "Content-Type: application/json" \\
  -d '{
    "model": "claude-sonnet-5",
    "messages": [{"role": "user", "content": "Hello"}],
    "stream": true
  }'`,
    node: `import OpenAI from "openai"

const client = new OpenAI({
  apiKey: "sk-••••",
  baseURL: "${baseUrl}",
})

const stream = await client.chat.completions.create({
  model: "claude-sonnet-5",
  messages: [{ role: "user", content: "Hello" }],
  stream: true,
})`,
  }

  const copy = () => {
    void navigator.clipboard?.writeText(snippets[sample]).then(
      () => setCopied(true),
      () => setCopied(false)
    )
  }

  return (
    <section className='px-6 py-16 md:py-24'>
      <div className='mx-auto grid max-w-6xl items-start gap-12 lg:grid-cols-[0.9fr_1.1fr]'>
        <AnimateInView>
          <Eyebrow>{t('Quick start')}</Eyebrow>
          <SectionHeading className='mt-5'>
            <span className='inline-block'>{t('Three steps,')}</span>{' '}
            <span className={`${homeAccent} inline-block`}>
              {t('then you can call')}
            </span>
          </SectionHeading>
          <p className={`mt-4 max-w-md text-sm leading-relaxed ${homeMuted}`}>
            {t('No architecture change — swapping the base URL is enough.')}
          </p>

          <ol className='mt-10 space-y-7'>
            {steps.map((step) => (
              <li key={step.num} className='flex gap-4'>
                <span className='home-display mt-0.5 inline-flex size-7 shrink-0 items-center justify-center rounded-lg bg-[#0b1c47] text-xs font-bold text-white dark:bg-[#eef1f6] dark:text-[#0b1c47]'>
                  {step.num.replace(/^0/, '')}
                </span>
                <div className='min-w-0'>
                  <h3 className='text-base font-bold text-[#0b1c47] dark:text-[#eef1f6]'>
                    {step.title}
                  </h3>
                  <p
                    className={`mt-1.5 max-w-md text-sm leading-relaxed text-pretty ${homeMuted}`}
                  >
                    {step.desc}
                  </p>
                  {step.chip ? (
                    <code className='mt-2.5 inline-block max-w-full truncate rounded-md bg-black/5 px-2 py-1 font-mono text-xs text-[#0b1c47] dark:bg-white/8 dark:text-[#eef1f6]'>
                      {step.chip}
                    </code>
                  ) : null}
                </div>
              </li>
            ))}
          </ol>

          <p
            className={`mt-9 flex flex-wrap items-center gap-1.5 text-xs ${homeMuted}`}
          >
            <Check className={`size-3.5 ${homeAccent}`} aria-hidden />
            {t('Claude · GPT · Gemini · DeepSeek · Qwen · GLM …')}
          </p>
        </AnimateInView>

        <AnimateInView delay={100}>
          <div className='overflow-hidden rounded-xl bg-[#0b1c47] text-[#eef1f6] shadow-[0_18px_50px_rgba(11,28,71,0.14)] ring-1 ring-black/8 dark:ring-white/10'>
            <div className='flex items-center gap-1 border-b border-white/10 p-3'>
              <div
                role='tablist'
                aria-label={t('Request sample')}
                className='flex gap-1'
              >
                {TABS.map((item) => (
                  <button
                    key={item.id}
                    type='button'
                    role='tab'
                    aria-selected={sample === item.id}
                    onClick={() => setSample(item.id)}
                    className={cn(
                      'h-9 rounded-full px-3.5 text-[13px] transition-colors duration-200 ease-out focus-visible:shadow-[0_0_0_3px_rgba(120,160,240,0.4)] focus-visible:outline-none',
                      sample === item.id
                        ? 'bg-white font-medium text-[#0b1c47]'
                        : 'text-[#eef1f6]/65 hover:bg-white/8'
                    )}
                  >
                    {item.label}
                  </button>
                ))}
              </div>
              <button
                type='button'
                onClick={copy}
                aria-label={t('Copy code')}
                className='ml-auto inline-flex size-9 items-center justify-center rounded-full text-[#eef1f6]/60 transition-colors duration-200 hover:bg-white/8 hover:text-[#eef1f6] focus-visible:shadow-[0_0_0_3px_rgba(120,160,240,0.4)] focus-visible:outline-none'
              >
                {copied ? (
                  <Check className='size-4 text-[#78a0f0]' aria-hidden />
                ) : (
                  <Copy className='size-4' aria-hidden />
                )}
              </button>
            </div>
            <pre className='overflow-x-auto p-5 font-mono text-[13px] leading-relaxed whitespace-pre'>
              <code>
                <Highlighted code={snippets[sample]} />
              </code>
            </pre>
          </div>
        </AnimateInView>
      </div>
    </section>
  )
}

const KEYWORDS =
  'from|import|const|let|var|async|await|new|return|export|default|True|False|None|true|false|null'

const TOKEN = new RegExp(
  [
    '(#[^\\n]*|//[^\\n]*)', // 1 注释
    '("(?:[^"\\\\]|\\\\.)*"|\'(?:[^\'\\\\]|\\\\.)*\')', // 2 字符串
    `\\b(${KEYWORDS})\\b`, // 3 关键字
    '\\b(\\d+(?:\\.\\d+)?)\\b', // 4 数字
    '(-[A-Za-z]\\b)', // 5 命令行开关
    '([A-Za-z_$][\\w$]*)(?=\\()', // 6 调用
  ].join('|'),
  'g'
)

const CLASS = [
  '',
  'text-[#7c8698]', // 注释
  'text-[#7fd1e8]', // 字符串
  'text-[#c9a6f2]', // 关键字
  'text-[#e0a878]', // 数字
  'text-[#e0a878]', // 命令行开关
  'text-[#8ab4f8]', // 调用
]

/** 只给三段固定示例做着色，不值得为此引入完整高亮器 */
function Highlighted(props: { code: string }) {
  const out: React.ReactNode[] = []
  let cursor = 0
  let key = 0

  for (const match of props.code.matchAll(TOKEN)) {
    const at = match.index
    if (at > cursor) out.push(props.code.slice(cursor, at))
    const group = match.slice(1).findIndex((v) => v !== undefined) + 1
    out.push(
      <span key={key++} className={CLASS[group]}>
        {match[0]}
      </span>
    )
    cursor = at + match[0].length
  }
  if (cursor < props.code.length) out.push(props.code.slice(cursor))

  return <Fragment>{out}</Fragment>
}
