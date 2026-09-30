package model

import "sort"

// RefreshIQChannelCache refreshes from committed rows, never from a stale run snapshot.
func RefreshIQChannelCache(id int) error {
	var ch Channel
	if err := DB.First(&ch, id).Error; err != nil {
		return err
	}
	var abilities []Ability
	if err := DB.Where("channel_id = ? AND enabled = ?", id, true).Find(&abilities).Error; err != nil {
		return err
	}
	channelSyncLock.Lock()
	defer channelSyncLock.Unlock()
	if channelsIDM == nil {
		return nil
	}
	channelsIDM[id] = &ch
	for _, models := range group2model2channels {
		for name, ids := range models {
			filtered := make([]int, 0, len(ids))
			for _, existing := range ids {
				if existing != id {
					filtered = append(filtered, existing)
				}
			}
			models[name] = filtered
		}
	}
	for _, ability := range abilities {
		if group2model2channels[ability.Group] == nil {
			group2model2channels[ability.Group] = make(map[string][]int)
		}
		ids := append(group2model2channels[ability.Group][ability.Model], id)
		sort.SliceStable(ids, func(i, j int) bool { return channelsIDM[ids[i]].GetPriority() > channelsIDM[ids[j]].GetPriority() })
		group2model2channels[ability.Group][ability.Model] = ids
	}
	return nil
}
