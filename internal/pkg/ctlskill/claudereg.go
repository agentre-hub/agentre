package ctlskill

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// registerPlugin 把插件登记进 Claude Code 的三份用户级 JSON：
// installed_plugins.json（scope user + installPath + 版本与时间戳）、
// known_marketplaces.json（directory 源）、settings.json（enabledPlugins 写 false —
// 全局关闭，逐档授权由「组织架构 → 管理技能」写进各 agent 的 skills_json）。
//
// 三份文件先全部读+解析、再全部写回：任何一份坏了都直接返回错误，一个字节也不落，
// 用户原有的插件/marketplace/设置项因此不会被半途覆盖。合并式写入，只碰自己的键。
func registerPlugin(home string, opts Options) error {
	installedPath := filepath.Join(pluginsDir(home), "installed_plugins.json")
	knownPath := filepath.Join(pluginsDir(home), "known_marketplaces.json")
	settings := settingsPath(home)

	installed, err := loadJSONObject(installedPath)
	if err != nil {
		return err
	}
	known, err := loadJSONObject(knownPath)
	if err != nil {
		return err
	}
	config, err := loadJSONObject(settings)
	if err != nil {
		return err
	}

	now := time.Now().UTC().Format(time.RFC3339Nano)
	marketplace := MarketplaceDir(home)
	source := map[string]any{"source": "directory", "path": marketplace}

	mergeInstalledPlugin(installed, PluginDir(home), opts.Version, now)
	known[MarketplaceName] = map[string]any{
		"source":          source,
		"installLocation": marketplace,
		"lastUpdated":     now,
	}
	subObject(config, "enabledPlugins")[PluginID] = false
	subObject(config, "extraKnownMarketplaces")[MarketplaceName] = map[string]any{"source": source}

	if err := writeJSONFile(installedPath, installed); err != nil {
		return err
	}
	if err := writeJSONFile(knownPath, known); err != nil {
		return err
	}
	return writeJSONFile(settings, config)
}

// unregisterPlugin 从三份注册文件里只摘掉本插件/本 marketplace 的键，其余条目原样保留。
// 文件不存在即无可摘除。
func unregisterPlugin(home string) error {
	installedPath := filepath.Join(pluginsDir(home), "installed_plugins.json")
	knownPath := filepath.Join(pluginsDir(home), "known_marketplaces.json")
	settings := settingsPath(home)

	if err := editJSONObject(installedPath, func(installed map[string]any) {
		plugins, ok := installed["plugins"].(map[string]any)
		if !ok {
			return
		}
		if kept := withoutUserScope(plugins[PluginID]); len(kept) > 0 {
			plugins[PluginID] = kept
		} else {
			delete(plugins, PluginID)
		}
	}); err != nil {
		return err
	}
	if err := editJSONObject(knownPath, func(known map[string]any) {
		delete(known, MarketplaceName)
	}); err != nil {
		return err
	}
	return editJSONObject(settings, func(config map[string]any) {
		if enabled, ok := config["enabledPlugins"].(map[string]any); ok {
			delete(enabled, PluginID)
		}
		if extra, ok := config["extraKnownMarketplaces"].(map[string]any); ok {
			delete(extra, MarketplaceName)
		}
	})
}

// mergeInstalledPlugin 在 installed_plugins.json 里更新（或新增）本插件的 user 档条目，
// 保留该条目上 CLI 自己写的其他字段与首次安装时间。
func mergeInstalledPlugin(installed map[string]any, pluginPath, version, now string) {
	if _, ok := installed["version"]; !ok {
		installed["version"] = 2
	}
	plugins := subObject(installed, "plugins")
	entries, _ := plugins[PluginID].([]any)
	for i, raw := range entries {
		entry, ok := raw.(map[string]any)
		if !ok || entry["scope"] != "user" {
			continue
		}
		entry["installPath"] = pluginPath
		entry["version"] = version
		entry["lastUpdated"] = now
		if _, ok := entry["installedAt"]; !ok {
			entry["installedAt"] = now
		}
		entries[i] = entry
		plugins[PluginID] = entries
		return
	}
	plugins[PluginID] = append(entries, map[string]any{
		"scope":       "user",
		"installPath": pluginPath,
		"version":     version,
		"installedAt": now,
		"lastUpdated": now,
	})
}

// withoutUserScope 去掉本插件的 user 档条目，保留其他 scope（项目级安装不归我们管）。
func withoutUserScope(raw any) []any {
	entries, _ := raw.([]any)
	kept := make([]any, 0, len(entries))
	for _, item := range entries {
		if entry, ok := item.(map[string]any); ok && entry["scope"] == "user" {
			continue
		}
		kept = append(kept, item)
	}
	return kept
}

// subObject 取（必要时新建）一个对象型子字段；原值不是对象时按缺失处理。
func subObject(parent map[string]any, key string) map[string]any {
	child, ok := parent[key].(map[string]any)
	if !ok {
		child = make(map[string]any)
		parent[key] = child
	}
	return child
}

// loadJSONObject 读一份 JSON 对象；文件不存在按空对象处理，内容损坏则返回错误
// （由调用方降级成一条 warn，绝不覆盖写掉用户看不懂的那份文件）。
func loadJSONObject(path string) (map[string]any, error) {
	data, err := os.ReadFile(path) // #nosec G304 -- path 由 home 拼接的固定配置路径，非用户输入。
	if os.IsNotExist(err) {
		return make(map[string]any), nil
	}
	if err != nil {
		return nil, fmt.Errorf("ctlskill: read %s: %w", filepath.Base(path), err)
	}
	value := make(map[string]any)
	if err := json.Unmarshal(data, &value); err != nil {
		return nil, fmt.Errorf("ctlskill: parse %s: %w", filepath.Base(path), err)
	}
	return value, nil
}

// editJSONObject 就地改写一份已存在的 JSON 对象文件；文件不存在则什么也不做。
func editJSONObject(path string, edit func(map[string]any)) error {
	if !fileExists(path) {
		return nil
	}
	value, err := loadJSONObject(path)
	if err != nil {
		return err
	}
	edit(value)
	return writeJSONFile(path, value)
}
