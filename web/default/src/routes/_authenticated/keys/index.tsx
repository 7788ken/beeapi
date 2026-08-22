import z from 'zod'
import { createFileRoute } from '@tanstack/react-router'
import { ApiKeys } from '@/features/keys'
import { API_KEY_STATUS_OPTIONS } from '@/features/keys/constants'

const apiKeySearchSchema = z.object({
  page: z.number().optional().catch(1),
  pageSize: z.number().optional().catch(10),
  status: z
    .array(z.enum(API_KEY_STATUS_OPTIONS.map((s) => s.value as `${number}`)))
    .optional()
    .catch([]),
  filter: z.string().optional().catch(''),
  // ?group=NAME deep link from Group Square — auto-opens the create drawer with
  // the group field pre-filled, then strips itself from the URL on consume.
  group: z.string().optional().catch(undefined),
})

export const Route = createFileRoute('/_authenticated/keys/')({
  validateSearch: apiKeySearchSchema,
  component: ApiKeys,
})
