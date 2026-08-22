export interface GroupInfo {
  ratio: number
  desc?: string
}

export interface GroupRecord {
  name: string
  ratio: number
  desc: string
}

export type SelfGroupsResponse = Record<string, GroupInfo>

export interface GroupUptimeBucket {
  ts: number
  request_count: number
  success_count: number
  success_rate: number
}

export type GroupUptimeResponse = Record<string, GroupUptimeBucket[]>
