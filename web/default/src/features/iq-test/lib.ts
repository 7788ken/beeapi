import { isAxiosError } from 'axios'

export function iqTime(timestamp: number | null | undefined): string {
  return timestamp ? new Date(timestamp * 1000).toLocaleString() : '-'
}

export function iqErrorMessage(error: Error): string {
  if (isAxiosError<{ message?: string }>(error))
    return error.response?.data.message || error.message
  return error.message
}
