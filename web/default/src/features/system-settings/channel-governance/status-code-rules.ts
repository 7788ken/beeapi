import * as z from 'zod'
import { parseHttpStatusCodeRules } from '@/lib/http-status-code-rules'

export const statusCodeRules = z.string().superRefine((value, ctx) => {
  const parsed = parseHttpStatusCodeRules(value ?? '')
  if (!parsed.ok) {
    ctx.addIssue({
      code: 'custom',
      message: `Invalid status code rules: ${parsed.invalidTokens.join(', ')}`,
    })
  }
})
