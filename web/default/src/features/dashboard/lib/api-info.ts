import type { PingStatus } from '@/features/dashboard/types'

/**
 * Get color class for latency status
 */
export function getLatencyColorClass(latency: number): string {
  if (latency < 200) {
    return 'text-green-600 dark:text-green-400'
  }
  if (latency < 500) {
    return 'text-yellow-600 dark:text-yellow-400'
  }
  return 'text-red-600 dark:text-red-400'
}

/**
 * 地址所在的服务器。同一域名带不带 /v1 走的是同一条线路，测速结果按它归并。
 * 配置里写坏的地址原样返回，测速时显示连不上。
 */
export function urlOrigin(url: string): string {
  try {
    return new URL(url).origin
  } catch {
    return url
  }
}

const PING_SAMPLES = 3
const PING_TIMEOUT_MS = 8000

async function timeRequest(url: string): Promise<number> {
  // 必须是绝对地址：不带协议的地址会被 fetch 当成当前页的相对路径，测到的是控制台自己
  const target = new URL(url)
  const startTime = performance.now()
  await fetch(target, {
    method: 'HEAD',
    mode: 'no-cors',
    cache: 'no-store',
    signal: AbortSignal.timeout(PING_TIMEOUT_MS),
  })
  return performance.now() - startTime
}

/**
 * Test URL latency
 * 先发一次预热请求付掉 DNS / TCP / TLS 建连，再取几次往返的中位数。
 * 只测一次时首个请求多付建连时间（实测 1106ms 对 240ms），线路之间没法比。
 */
export async function testUrlLatency(url: string): Promise<PingStatus> {
  try {
    await timeRequest(url)
    const samples: number[] = []
    for (let i = 0; i < PING_SAMPLES; i++) {
      samples.push(await timeRequest(url))
    }
    samples.sort((a, b) => a - b)
    const latency = Math.round(samples[Math.floor(PING_SAMPLES / 2)])

    return { latency, testing: false, error: false }
  } catch (_error) {
    return { latency: null, testing: false, error: true }
  }
}

/**
 * Open external speed test link
 */
export function openExternalSpeedTest(url: string): void {
  const encodedUrl = encodeURIComponent(url)
  const speedTestUrl = `https://www.tcptest.cn/http/${encodedUrl}`
  window.open(speedTestUrl, '_blank', 'noopener,noreferrer')
}

/**
 * Get default ping status
 */
export function getDefaultPingStatus(): PingStatus {
  return {
    latency: null,
    testing: false,
    error: false,
  }
}
