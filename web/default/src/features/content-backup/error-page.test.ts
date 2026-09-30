// @ts-expect-error -- bun:test 的类型未随仓库安装，测试由 bun 运行器执行
import { expect, test } from 'bun:test'

import { shouldShowErrorPage } from '@/lib/query-error'

import { CONTENT_BACKUP_QUERY_META } from './api'

// 设计文档 9.3：「接口失败显示真实状态而非假 0/成功」。
// 后台模板对任何 500 都会 router.navigate('/500')，把整个管理页面换成错误页——
// 节点面板里那段「保留上次成功数据 + 失败提示」永远到不了，运营看到的不是
// 真实状态，而是一张与内容备份无关的通用错误页。
test('普通查询的 500 仍然走全站错误页（默认行为不变）', () => {
  expect(shouldShowErrorPage(500, undefined)).toBe(true)
})

test('内容备份的查询在页面内显示失败，不掀掉整个后台', () => {
  expect(shouldShowErrorPage(500, CONTENT_BACKUP_QUERY_META)).toBe(false)
})

test('非 500 本来就不跳错误页', () => {
  expect(shouldShowErrorPage(503, undefined)).toBe(false)
  expect(shouldShowErrorPage(undefined, undefined)).toBe(false)
})
