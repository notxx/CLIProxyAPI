# 使用统计持久化功能

## 概述
将使用统计数据持久化到文件，以便在服务重启后数据不丢失。

## 实现方案

### 1. 配置

在 `internal/config/config.go` 中添加 `UsageStatisticsConfig` 结构体：

```yaml
usage-statistics:
  enabled: true                    # 启用内存统计收集
  persist-file: "~/.cli-proxy-api/usage.json"  # 持久化文件路径
  save-interval: "5m"              # 自动保存间隔（时间字符串）
  restore-on-start: true           # 启动时自动恢复
```

### 2. 新增文件：internal/usage/file_plugin.go

实现 `FileUsagePlugin`，功能包括：
- 实现 `coreusage.Plugin` 接口
- 定期将统计数据保存到 JSON 文件
- 启动时从文件加载统计数据
- 使用原子文件写入（临时文件 + 重命名）保证安全
- 优雅处理文件 I/O 错误（仅记录日志）

核心方法：
- `NewFileUsagePlugin(filePath string, interval time.Duration) *FileUsagePlugin`
- `HandleUsage(ctx, record)` - 空操作（数据通过 LoggerPlugin 流转）
- `Start()` - 启动后台保存协程
- `Stop()` - 停止协程并执行最终保存
- `Load() error` - 启动时从文件恢复
- `Save() error` - 将当前统计写入文件

### 3. 修改的文件

**internal/usage/logger_plugin.go：**
- 添加 `GetSnapshot()` 辅助方法
- 导出 `MergeSnapshot()` 供外部使用

**internal/config/config.go：**
- 添加 `UsageStatisticsConfig` 结构体
- 添加验证和默认值
- 更新 `config.example.yaml` 注释

**cmd/server/main.go：**
- 如果设置了 `persist-file`，初始化 FileUsagePlugin
- 在后台启动插件
- 优雅关闭时停止插件

**config.example.yaml：**
- 添加 usage-statistics 配置节

### 4. 数据流

```
请求 → Executor → usage.PublishRecord()
                           ↓
                    LoggerPlugin（内存聚合）
                           ↓
                    FileUsagePlugin（定期保存）
                           ↓
                    ~/.cli-proxy-api/usage.json
```

### 5. 文件格式

与现有 `StatisticsSnapshot` 兼容的 JSON 格式：
```json
{
  "version": 1,
  "saved_at": "2026-02-02T12:30:00Z",
  "data": {
    "total_requests": 100,
    "success_count": 95,
    "failure_count": 5,
    "total_tokens": 50000,
    "apis": {...},
    "requests_by_day": {...}
  }
}
```

### 6. 错误处理

- 启动时加载失败：记录警告，以空统计开始
- 保存失败：记录错误，下次间隔重试
- JSON 损坏：备份损坏文件，重新开始
- 路径解析：支持 `~` 表示家目录

### 7. 测试清单

- [ ] 配置解析与默认值
- [ ] 文件保存/加载往返
- [ ] 并发访问安全
- [ ] 优雅关闭时保存数据
- [ ] 损坏文件处理
- [ ] 家目录展开
