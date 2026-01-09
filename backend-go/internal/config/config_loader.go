package config

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/fsnotify/fsnotify"
)

const (
	maxBackups      = 10
	keyRecoveryTime = 5 * time.Minute
	maxFailureCount = 3
)

// NewConfigManager 创建配置管理器
func NewConfigManager(configFile string) (*ConfigManager, error) {
	cm := &ConfigManager{
		configFile:      configFile,
		failedKeysCache: make(map[string]*FailedKey),
		keyIndex:        make(map[string]int),
		keyRecoveryTime: keyRecoveryTime,
		maxFailureCount: maxFailureCount,
		stopChan:        make(chan struct{}),
	}

	// 加载配置
	if err := cm.loadConfig(); err != nil {
		return nil, err
	}

	// 启动文件监听
	if err := cm.startWatcher(); err != nil {
		log.Printf("[Config-Watcher] 警告: 启动配置文件监听失败: %v", err)
	}

	// 启动定期清理
	cm.wg.Add(1)
	go func() {
		defer cm.wg.Done()
		cm.cleanupExpiredFailures()
	}()

	return cm, nil
}

// loadConfig 加载配置
func (cm *ConfigManager) loadConfig() error {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	// 如果配置文件不存在，创建默认配置
	if _, err := os.Stat(cm.configFile); os.IsNotExist(err) {
		return cm.createDefaultConfig()
	}

	// 读取配置文件
	data, err := os.ReadFile(cm.configFile)
	if err != nil {
		return err
	}

	// 关键：不要对 cm.config 做就地 Unmarshal。
	// 热重载期间，可能仍有并发请求持有旧快照里的 slice/map 引用；
	// 就地写入会直接改动旧内存，触发数据竞争。
	var newConfig Config
	if err := json.Unmarshal(data, &newConfig); err != nil {
		return err
	}

	// 兼容旧配置：检查 FuzzyModeEnabled 字段是否存在
	// 如果不存在，默认设为 true（新功能默认启用）
	needSaveDefaults := cm.applyConfigDefaults(&newConfig, data)

	// 兼容旧格式：检测是否需要迁移
	needMigration := cm.migrateOldFormat(&newConfig)

	// 如果有默认值迁移或格式迁移，保存配置
	saved := false
	if needSaveDefaults || needMigration {
		if err := cm.saveConfigLocked(newConfig); err != nil {
			log.Printf("[Config-Migration] 警告: 保存迁移后的配置失败: %v", err)
			return err
		}
		saved = true
		newConfig = cm.config
		if needMigration {
			log.Printf("[Config-Migration] 配置迁移完成")
		}
	}

	// 自检：没有配置 key 的渠道自动暂停
	if cm.validateChannelKeys(&newConfig) {
		if err := cm.saveConfigLocked(newConfig); err != nil {
			log.Printf("[Config-Validate] 警告: 保存自检后的配置失败: %v", err)
			return err
		}
		saved = true
		newConfig = cm.config
	}

	// 未触发 saveConfigLocked 时，只更新内存快照（一次性替换）。
	if !saved {
		cm.config = newConfig
	}

	return nil
}

// createDefaultConfig 创建默认配置
func (cm *ConfigManager) createDefaultConfig() error {
	defaultConfig := Config{
		Upstream:                 []UpstreamConfig{},
		CurrentUpstream:          0,
		LoadBalance:              "failover",
		ResponsesUpstream:        []UpstreamConfig{},
		CurrentResponsesUpstream: 0,
		ResponsesLoadBalance:     "failover",
		GeminiUpstream:           []UpstreamConfig{},
		GeminiLoadBalance:        "failover",
		FuzzyModeEnabled:         true, // 默认启用 Fuzzy 模式
	}

	if err := os.MkdirAll(filepath.Dir(cm.configFile), 0755); err != nil {
		return err
	}

	return cm.saveConfigLocked(defaultConfig)
}

// applyConfigDefaults 应用配置默认值
// rawJSON: 原始 JSON 数据，用于检测字段是否存在
// 返回: 是否有字段需要迁移（需要保存配置）
func (cm *ConfigManager) applyConfigDefaults(cfg *Config, rawJSON []byte) bool {
	needSave := false

	if cfg.LoadBalance == "" {
		cfg.LoadBalance = "failover"
	}
	if cfg.ResponsesLoadBalance == "" {
		cfg.ResponsesLoadBalance = cfg.LoadBalance
	}
	if cfg.GeminiLoadBalance == "" {
		cfg.GeminiLoadBalance = "failover"
	}

	// FuzzyModeEnabled 默认值处理：
	// 由于 bool 零值是 false，无法区分"用户设为 false"和"字段不存在"
	// 通过检查原始 JSON 是否包含该字段来判断
	var rawMap map[string]json.RawMessage
	if err := json.Unmarshal(rawJSON, &rawMap); err == nil {
		if _, exists := rawMap["fuzzyModeEnabled"]; !exists {
			// 字段不存在，设为默认值 true
			cfg.FuzzyModeEnabled = true
			needSave = true
			log.Printf("[Config-Migration] FuzzyModeEnabled 字段不存在，设为默认值 true")
		}
	}

	return needSave
}

// migrateOldFormat 迁移旧格式配置，返回是否有迁移
func (cm *ConfigManager) migrateOldFormat(cfg *Config) bool {
	needMigration := false

	// 迁移 Messages 渠道
	if cm.migrateUpstreams(cfg.Upstream, cfg.CurrentUpstream, "Messages") {
		needMigration = true
	}

	// 迁移 Responses 渠道
	if cm.migrateUpstreams(cfg.ResponsesUpstream, cfg.CurrentResponsesUpstream, "Responses") {
		needMigration = true
	}

	if needMigration {
		log.Printf("[Config-Migration] 检测到旧格式配置，正在迁移到新格式...")
	}

	return needMigration
}

// migrateUpstreams 迁移单个渠道列表
func (cm *ConfigManager) migrateUpstreams(upstreams []UpstreamConfig, currentIdx int, name string) bool {
	if len(upstreams) == 0 {
		return false
	}

	// 检查是否已有 status 字段
	for _, up := range upstreams {
		if up.Status != "" {
			return false
		}
	}

	// 需要迁移
	if currentIdx < 0 || currentIdx >= len(upstreams) {
		currentIdx = 0
	}

	for i := range upstreams {
		if i == currentIdx {
			upstreams[i].Status = "active"
		} else {
			upstreams[i].Status = "disabled"
		}
	}

	log.Printf("[Config-Migration] %s 渠道 [%d] %s 已设置为 active，其他 %d 个渠道已设为 disabled",
		name, currentIdx, upstreams[currentIdx].Name, len(upstreams)-1)

	return true
}

// validateChannelKeys 自检渠道密钥配置
// 没有配置 API key 的渠道，即使状态为 active 也应暂停
// 返回 true 表示有配置被修改，需要保存
func (cm *ConfigManager) validateChannelKeys(cfg *Config) bool {
	modified := false

	// 检查 Messages 渠道
	for i := range cfg.Upstream {
		upstream := &cfg.Upstream[i]
		status := upstream.Status
		if status == "" {
			status = "active"
		}

		// 如果是 active 状态但没有配置 key，自动设为 suspended
		if status == "active" && len(upstream.APIKeys) == 0 {
			upstream.Status = "suspended"
			modified = true
			log.Printf("[Config-Validate] 警告: Messages 渠道 [%d] %s 没有配置 API key，已自动暂停", i, upstream.Name)
		}
	}

	// 检查 Responses 渠道
	for i := range cfg.ResponsesUpstream {
		upstream := &cfg.ResponsesUpstream[i]
		status := upstream.Status
		if status == "" {
			status = "active"
		}

		// 如果是 active 状态但没有配置 key，自动设为 suspended
		if status == "active" && len(upstream.APIKeys) == 0 {
			upstream.Status = "suspended"
			modified = true
			log.Printf("[Config-Validate] 警告: Responses 渠道 [%d] %s 没有配置 API key，已自动暂停", i, upstream.Name)
		}
	}

	// 检查 Gemini 渠道
	for i := range cfg.GeminiUpstream {
		upstream := &cfg.GeminiUpstream[i]
		status := upstream.Status
		if status == "" {
			status = "active"
		}

		// 如果是 active 状态但没有配置 key，自动设为 suspended
		if status == "active" && len(upstream.APIKeys) == 0 {
			upstream.Status = "suspended"
			modified = true
			log.Printf("[Config-Validate] 警告: Gemini 渠道 [%d] %s 没有配置 API key，已自动暂停", i, upstream.Name)
		}
	}

	return modified
}

// saveConfigLocked 保存配置（已加锁）
func (cm *ConfigManager) saveConfigLocked(config Config) error {
	// 备份当前配置
	cm.backupConfig()

	// 清理已废弃字段，确保不会被序列化到 JSON
	config.CurrentUpstream = 0
	config.CurrentResponsesUpstream = 0

	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return err
	}

	cm.config = config
	return os.WriteFile(cm.configFile, data, 0644)
}

// SaveConfig 保存配置
func (cm *ConfigManager) SaveConfig() error {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	return cm.saveConfigLocked(cm.config)
}

// backupConfig 备份配置
func (cm *ConfigManager) backupConfig() {
	if _, err := os.Stat(cm.configFile); os.IsNotExist(err) {
		return
	}

	backupDir := filepath.Join(filepath.Dir(cm.configFile), "backups")
	if err := os.MkdirAll(backupDir, 0755); err != nil {
		log.Printf("[Config-Backup] 警告: 创建备份目录失败: %v", err)
		return
	}

	// 读取当前配置
	data, err := os.ReadFile(cm.configFile)
	if err != nil {
		log.Printf("[Config-Backup] 警告: 读取配置文件失败: %v", err)
		return
	}

	// 创建备份文件
	timestamp := time.Now().Format("2006-01-02T15-04-05")
	backupFile := filepath.Join(backupDir, fmt.Sprintf("config-%s.json", timestamp))
	if err := os.WriteFile(backupFile, data, 0644); err != nil {
		log.Printf("[Config-Backup] 警告: 写入备份文件失败: %v", err)
		return
	}

	// 清理旧备份
	cm.cleanupOldBackups(backupDir)
}

// cleanupOldBackups 清理旧备份
func (cm *ConfigManager) cleanupOldBackups(backupDir string) {
	entries, err := os.ReadDir(backupDir)
	if err != nil {
		return
	}

	if len(entries) <= maxBackups {
		return
	}

	// 删除最旧的备份
	for i := 0; i < len(entries)-maxBackups; i++ {
		os.Remove(filepath.Join(backupDir, entries[i].Name()))
	}
}

// startWatcher 启动文件监听
func (cm *ConfigManager) startWatcher() error {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}

	cm.watcher = watcher

	cm.wg.Add(1)
	go func() {
		defer cm.wg.Done()
		for {
			select {
			case <-cm.stopChan:
				return
			case event, ok := <-watcher.Events:
				if !ok {
					return
				}
				select {
				case <-cm.stopChan:
					return
				default:
				}
				if event.Op&fsnotify.Write == fsnotify.Write {
					log.Printf("[Config-Watcher] 检测到配置文件变化，重载配置...")
					if err := cm.loadConfig(); err != nil {
						log.Printf("[Config-Watcher] 警告: 配置重载失败: %v", err)
					} else {
						log.Printf("[Config-Watcher] 配置已重载")
					}
				}
			case err, ok := <-watcher.Errors:
				if !ok {
					return
				}
				select {
				case <-cm.stopChan:
					return
				default:
				}
				log.Printf("[Config-Watcher] 警告: 文件监听错误: %v", err)
			}
		}
	}()

	return watcher.Add(cm.configFile)
}

// Close 关闭 ConfigManager 并释放资源（幂等，可安全多次调用）
func (cm *ConfigManager) Close() error {
	var closeErr error
	cm.closeOnce.Do(func() {
		// 通知所有 goroutine 停止
		if cm.stopChan != nil {
			close(cm.stopChan)
		}

		// 关闭文件监听器
		if cm.watcher != nil {
			closeErr = cm.watcher.Close()
		}

		// 等待后台 goroutine 退出，避免 Close 后仍写入备份/配置文件
		cm.wg.Wait()
	})
	return closeErr
}
