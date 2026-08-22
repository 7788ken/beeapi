import { useEffect } from 'react'
import { useNavigate, useSearch } from '@tanstack/react-router'
import { useApiKeys } from './api-keys-provider'

/**
 * Listens to `?group=NAME` deep link from the Group Square page.
 * On the first render that has it set, opens the create drawer with the group
 * pre-filled, then strips the param from the URL so refreshes don't keep
 * re-opening the drawer.
 *
 * Renders nothing.
 */
export function PresetGroupEffect() {
  const navigate = useNavigate()
  const search = useSearch({ from: '/_authenticated/keys/' })
  const { open, setOpen, setPresetGroup } = useApiKeys()
  const groupParam = (search as { group?: string }).group

  useEffect(() => {
    if (!groupParam) return
    // Skip if a drawer is already open (avoid stomping on user's in-progress edit)
    if (open) {
      // Still strip the URL param so a refresh doesn't re-trigger.
      void navigate({
        to: '.',
        search: (prev: Record<string, unknown>) => ({
          ...prev,
          group: undefined,
        }),
        replace: true,
      })
      return
    }
    setPresetGroup(groupParam)
    setOpen('create')
    void navigate({
      to: '.',
      search: (prev: Record<string, unknown>) => ({
        ...prev,
        group: undefined,
      }),
      replace: true,
    })
    // We deliberately consume only the first non-empty groupParam: subsequent
    // renders should not re-fire even if other deps change.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [groupParam])

  return null
}
