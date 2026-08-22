export const ADMIN_LOG_LIST_PATH = '/api/log/'

export function buildApiPath(endpoint: string, isAdmin: boolean): string {
  return isAdmin ? endpoint : `${endpoint}/self`
}
