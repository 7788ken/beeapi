// @ts-expect-error -- bun:test 的类型未随仓库安装，测试由 bun 运行器执行
import { describe, expect, test } from 'bun:test'

import { refetchPolicy } from './api'

// 设计文档 9.3：隐藏页停止轮询；抽屉打开、有选中目标时不刷新，
// 否则正在看的详情和已勾选的重试目标会被下一次轮询冲掉。
describe('refetchPolicy', () => {
  // bun 测试环境没有 DOM，直接替换全局 document，覆盖 typeof document 的判断分支
  const withHidden = (hidden: boolean | undefined, run: () => void) => {
    const g = globalThis as unknown as Record<string, unknown>
    const had = 'document' in g
    const original = g.document
    if (hidden === undefined) delete g.document
    else g.document = { hidden }
    try {
      run()
    } finally {
      if (had) g.document = original
      else delete g.document
    }
  }

  test('页面可见且未 hold 时按给定间隔轮询', () => {
    withHidden(false, () => {
      expect(refetchPolicy({ pollMs: 15_000 })()).toBe(15_000)
      expect(refetchPolicy()()).toBe(30_000)
    })
  })

  test('页面隐藏时停止轮询', () => {
    withHidden(true, () => {
      expect(refetchPolicy({ pollMs: 15_000 })()).toBe(false)
      expect(refetchPolicy()()).toBe(false)
    })
  })

  test('hold 时停止轮询（抽屉打开 / 有选中目标）', () => {
    withHidden(false, () => {
      expect(refetchPolicy({ hold: true, pollMs: 15_000 })()).toBe(false)
    })
    // 即使拿不到 document（SSR / 测试环境），hold 也必须照样生效
    withHidden(undefined, () => {
      expect(refetchPolicy({ hold: true, pollMs: 15_000 })()).toBe(false)
    })
  })
})
