# packagegate — 供应商离线压缩包入库工具

`packagegate` 对供应商离线交付的 ZIP 包执行“先核对、后发布”的入库流程：
只有当清单、条目、路径、长度、摘要与 ZIP 完整性全部通过后，才把数据文件
发布到目标目录；任何失败都不会留下半成品目标。仅依赖 Go 标准库与本地文件，
不访问任何在线服务。

## 目录结构

- `cmd/packagegate`：入库命令行入口。
- `cmd/makesample`：构造符合规范的示例 ZIP 包，便于体验与联调。
- `internal/gate`：校验、限流、暂存与发布的核心实现。

## 清单格式

ZIP 根目录必须有且仅有一个 `manifest.json`，清单本身不列在 `files` 中；
没有数据文件时 `files` 为空数组 `[]`。每个条目包含：

- `path`：相对路径，只允许英文字母、数字、`.`、`_`、`-` 和 `/`。
- `length`：非负整数，文件原始字节数。
- `sha256`：原始字节的 64 位十六进制 SHA-256 摘要（小写）。

```json
{
  "files": [
    {
      "path": "data/numbers.csv",
      "length": 12,
      "sha256": "fb46cc821837f376e752a749250b1f04f30342de9abd0db66ed19ac336adbdb6"
    }
  ]
}
```

## 校验规则

- 根目录有且仅有一个 `manifest.json`；每个数据文件恰好对应一个 ZIP 条目，
  不允许漏列、多列、重复声明或额外条目。
- 路径拒绝绝对路径、反斜杠、冒号、空路径段、`.`/`..` 段、尾部点文件名以及
  Windows 保留设备名（如 `CON`、`NUL.txt`）。
- 忽略大小写重名（`A.txt` 与 `a.TXT`）拒绝；文件与子路径冲突（`a` 与
  `a/b.txt`）拒绝。
- ZIP 只接受普通文件条目；显式目录、符号链接及其他特殊条目一律拒绝。
- 解压后逐字节重新计算长度与 SHA-256，并依赖 ZIP 自身 CRC 校验；不信任清单
  或压缩头中声明的长度。
- `-max-entries` 与 `-max-bytes` 均为正整数，计数包含清单自身；累计解压
  字节在读取过程中受限，超限立即终止，不会先无界解压再检查。
- 目标父目录必须已存在，目标目录必须尚不存在（文件或目录都拒绝）。所有检查
  通过后才在父目录的临时暂存区写入，再原子重命名发布；失败自动清理暂存内容。

## 运行方式

```powershell
# 1. 构造示例包
go run ./cmd/makesample -out sample.zip

# 2. 入库（目标目录必须尚不存在，其父目录必须存在）
go run ./cmd/packagegate -zip sample.zip -target ./delivered `
  -max-entries 1000 -max-bytes 104857600
```

成功时向标准输出打印 JSON 回执；失败时向标准错误输出原因并以非零码退出。
回执中 `files` 按路径排序，因此相同包的回执不受 ZIP 内部条目顺序影响；
`file_count` 与 `total_bytes` 只统计数据文件，不含清单：

```json
{
  "target": "C:\\inbox\\delivered",
  "file_count": 2,
  "total_bytes": 36,
  "files": [
    { "path": "data/numbers.csv", "length": 12, "sha256": "fb46..." },
    { "path": "docs/readme.txt", "length": 24, "sha256": "2c71..." }
  ]
}
```

发布后的目标目录中只包含清单声明的数据文件，保持相对路径与原始字节，
`manifest.json` 不写入目标目录。

## 自动化测试

```powershell
go test ./...
```

`internal/gate` 下的测试覆盖：成功入库与回执排序、空数据清单、SHA-256
摘要不符、长度不符、路径越界与非法字符、忽略大小写重名、文件/子路径冲突、
漏列文件、额外条目、重复声明、条目上限、解压字节上限、目标父目录缺失与
已有目标保护、目录/符号链接条目拒绝、清单缺失或损坏、条目顺序无关性，
以及失败后无目标、无残留文件。
