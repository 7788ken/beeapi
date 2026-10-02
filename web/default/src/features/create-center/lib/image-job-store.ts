import { create } from 'zustand'
import type { ImageJob } from '../types'

// 生图任务放在页面级 store：切到对话页、去管理 key 再回来，进行中的任务照常落结果、已出的图也还在。
// 不持久化：图片是大段 base64，任务里还带着完整 key；刷新或关闭页面即清空。
interface ImageJobState {
  jobs: ImageJob[]
  add: (job: ImageJob) => void
  patch: (id: string, next: Partial<ImageJob>) => void
}

export const useImageJobStore = create<ImageJobState>()((set) => ({
  jobs: [],
  add: (job) => set((s) => ({ jobs: [...s.jobs, job] })),
  patch: (id, next) =>
    set((s) => ({
      jobs: s.jobs.map((j) => (j.id === id ? { ...j, ...next } : j)),
    })),
}))
