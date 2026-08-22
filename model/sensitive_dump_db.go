package model

// MarkSensitiveDumpsCleaned 把这些 dump_path 关联的命中记录批量标记为 dump_exists=false。
// 由 service.runSensitiveDumpCleanOnce 在物理删盘后调用。
//
// 注意：分批 update，避免单条 IN 列表过长（MySQL 默认 max_allowed_packet）。
func MarkSensitiveDumpsCleaned(paths []string) error {
	if len(paths) == 0 {
		return nil
	}
	const batchSize = 200
	for i := 0; i < len(paths); i += batchSize {
		end := i + batchSize
		if end > len(paths) {
			end = len(paths)
		}
		if err := DB.Model(&SensitiveBlockLog{}).
			Where("dump_path IN ?", paths[i:end]).
			Update("dump_exists", false).Error; err != nil {
			return err
		}
	}
	return nil
}
