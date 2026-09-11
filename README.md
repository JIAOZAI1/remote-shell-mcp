# remote-shell-mcp

本地运行的跨平台 MCP Server。Agent 可以添加、查找服务器，通过服务器 ID 执行命令、操作文件及使用远端 tmux，无需管理 SSH 连接。

## 构建与启动

开发工具链：Go `1.27.1`。本地目标平台为 Linux、macOS、Windows；首版远端要求 POSIX shell、SSH 和 SFTP。持久化终端还需要远端安装 tmux。

```sh
go build -o remote-shell-mcp ./cmd/remote-shell-mcp
./remote-shell-mcp --config /absolute/path/config.json
```

复制 `configs/config.example.json` 到本地私有配置目录，修改所有路径：

- `known_hosts`：已存在的本地主机密钥文件。通过可信渠道核对指纹后由用户维护，不自动信任未知主机。
- `credentials`：认证引用到本地私钥文件的映射，支持未加密及加密私钥。私钥不通过 MCP 传递；请使用文件权限保护专用低权限密钥。暂不支持 SSH 密码认证、SSH Agent、跳板机或自动读取 OpenSSH config。
- `credential_passphrase_env`：可选，认证引用到私钥口令环境变量名称的映射，例如 `{"dev-key":"REMOTE_SHELL_DEV_KEY_PASSPHRASE"}`。配置只保存变量名，不保存口令；未加密私钥无需设置。加密私钥要求该环境变量存在于 MCP Server 进程环境中。

使用加密私钥时，可以在 Bash 中隐藏输入口令，然后从该环境启动 MCP 客户端或 Server（不要把真实口令写进命令历史）：

```bash
read -r -s -p 'Private key passphrase: ' REMOTE_SHELL_DEV_KEY_PASSPHRASE
printf '\n'
export REMOTE_SHELL_DEV_KEY_PASSPHRASE
./remote-shell-mcp --config "$HOME/.remote-shell-mcp/config.json"
unset REMOTE_SHELL_DEV_KEY_PASSPHRASE
```

已经运行的 GUI 客户端不会自动继承新环境变量，需要通过其安全启动环境注入并重启 MCP Server。环境变量也不是密钥保险库，应避免环境转储及调试日志泄漏。

默认配置路径为 `{user}/.remote-shell-mcp/config.json`，其中 `{user}` 是用户主目录（通过 `os.UserHomeDir()` 获取），可通过 `--config` 覆盖。目录需事先创建并限制访问权限。服务器记录保存在同目录的 `servers.json`。`servers.lock` 防止多个实例同时写配置；异常退出后，只能在确认没有进程运行时手动删除锁文件。

客户端配置示例（具体格式以客户端为准）：

```json
{
  "mcpServers": {
    "remote-shell": {
      "command": "/absolute/path/remote-shell-mcp",
      "args": ["--config", "/absolute/path/config.json"]
    }
  }
}
```

使用 stdio，不监听网络端口。stdout 专用于 MCP。

## 本地日志

所有工具调用（包括未知工具和参数校验失败）通过 slog 保存为本地 JSON Lines 日志，记录开始、结束、call_id、工具名称、server_id、状态和耗时。不记录原始参数、命令、文件内容、输出或错误详情，避免泄漏秘密。success 表示工具调用成功，不代表远程命令退出码为零或终端任务完成。

在 `config.json` 中可配置：

```json
"logging": {
  "directory": "logs",
  "retention_days": 30
}
```

- 本地指运行 MCP Server 的机器。`directory` 默认为配置文件旁的 `logs` 目录；相对路径基于配置文件目录，也支持绝对路径。修改配置后需重启。
- 按进程本地时区每天一个文件：`remote-shell-mcp-YYYY-MM-DD.jsonl`，同日重启追加写入，不覆盖。
- `retention_days` 默认 30，设为 0 也使用默认值；有效范围 1–36500。保留天数包含今天，例如 7 表示保留今天及前 6 个自然日。
- 启动时及跨天后的首次日志写入时清理过期文件。空闲时不定时清理；仅删除目录内符合上述文件命名且日期有效的过期普通文件，不删除其他文件、目录或符号链接。不同实例应使用独立日志目录。
- 新目录权限为 0700，新日志文件为 0600（受平台及 umask 影响）；已有目录和文件的权限不自动修改。日志按天而非大小轮转，请按调用量预留磁盘空间。
- 日志初始化失败会阻止启动；运行期间写入或轮转失败，在 stderr 告警并输出该条日志，下次写入重试，不静默丢弃。启动/退出诊断仍写 stderr。

## 工具

### 服务器

- `server_delete(server_id)`：原子持久化删除本地服务器配置；未知 ID 报错，写入失败保留原配置。不会删除凭证、远程文件或 tmux 会话，不中断已开始的操作；后续调用不能再使用该 ID。
- `server_add(name, host, user, credential_ref, workdir, port?)`：端口默认 22，工作目录必须是远端绝对路径。持久化后返回 `server_id`；不验证连通性，不自动接受主机密钥。重名拒绝。
- `server_list(query?)`：按名称或地址过滤，返回服务器标识和基本信息，不返回认证配置。

添加示例：

```json
{"name":"dev","host":"192.168.1.100","user":"deploy","credential_ref":"dev-key","workdir":"/srv/project"}
```

其他工具必须提供返回的 `server_id`。删除使用 `server_delete`；更新服务器暂未提供工具，可在停止程序后修改配置。

### 命令和文件

| 工具 | 参数（除 server_id 外） |
|---|---|
| `remote_exec` | `command`, `cwd?`, `timeout_seconds?` |
| `remote_read_file` | `path`, `offset?`, `limit?` |
| `remote_write_file` | `path`, `content`, `overwrite?`, `create_parents?` |
| `remote_list_dir` | `path`, `offset?`, `limit?` |
| `remote_upload` | `local_path`, `remote_path`, `overwrite?`, `create_parents?` |
| `remote_download` | `remote_path`, `local_path`, `overwrite?`, `create_parents?` |

- 远端相对路径基于服务器 `workdir`。工作目录不是安全沙箱。
- 本地路径支持绝对路径；相对路径基于 MCP Server 进程工作目录。本地指 MCP Server 所在机器，不是其他环境中的 Agent 客户端。不设置传输根目录限制，访问权限由本地操作系统决定。
- 命令调用相互独立，不分配 PTY，不保留上次调用的 shell 状态。返回 stdout、stderr、退出码、截断、超时和结果未知标记。非零退出码直接体现在结果中。
- 默认命令超时 60 秒，可设 1–600 秒。stdout 和 stderr 各保留最多 1 MiB，之后继续排空但丢弃数据。
- 文本读取最大 10 MiB，UTF-8，不接受 NUL。offset 为 1 起始行号，limit 默认 200、最大 10000。返回的行使用 LF 连接、不保留末尾换行，不用于字节保真复制；保真复制请下载。
- 文本写入最大 10 MiB。创建与覆盖均使用临时文件提交。默认不覆盖、默认不创建父目录。覆盖保留权限位，但不承诺 ACL、扩展属性、硬链接关系或所有权不变。
- 远端无覆盖提交需要 SFTP `hardlink@openssh.com`，覆盖需要 `posix-rename@openssh.com`；不支持则报错，不静默降级。拒绝覆盖最终路径为符号链接的文件。
- 目录 offset 从 0 开始，limit 默认 200、最大 10000；结果含 `next_offset`、`has_more`。目前会在内存读取整个目录，分页不是稳定快照，超大目录优化待实现。
- 上传下载只支持单个普通文件，最大 1 GiB；SFTP 操作总超时 10 分钟。下载通过本地临时文件提交，无覆盖使用硬链接，目标文件系统不支持时会报错。

### 持久化终端

| 工具 | 参数（除 server_id 外） |
|---|---|
| `remote_terminal_create` | `cwd?` |
| `remote_terminal_list` | 无 |
| `remote_terminal_send` | `terminal_id`, `text?`, `submit?`, `key?` |
| `remote_terminal_read` | `terminal_id`, `lines?` |
| `remote_terminal_close` | `terminal_id` |

每台服务器使用隔离的 tmux socket；只操作本项目会话。创建返回 `terminal_id`。输入操作串行化，文本最大 64 KiB。`key` 与文本/submit 互斥，支持 `Enter`、`C-c`、`C-d`、`Tab`、`Escape`。

`send` 只确认发送，不代表命令完成。`read` 捕获屏幕/历史快照，默认向前读取 200 行历史，最大 10000，另含当前屏幕；可能重复且受 tmux 历史容量限制，不提供独立 stdout/stderr/退出码。没有 tmux 服务或会话时，list 当前返回工具错误。

SSH 断开和本地进程退出不主动销毁 tmux 会话，重连后可列举并继续操作。远端重启不保证存活。`close` 会终止其中的任务。tmux 缺失时明确报错，不自动安装。

## 连接与安全边界

首版每次调用使用独立 SSH 连接，全局最多并发 4 个连接；连接/握手超时 10 秒。这样取消请求可以关闭自己的连接而不影响其他调用。连接复用、保活和可配置限制尚待实现。

请求取消或超时会关闭连接，但不能保证远端子进程终止。已发送的命令、输入、写入不自动重试；结果未知时应先检查实际状态。连接失败绝不回退本地执行。文件传输断线可能遗留 `.remote-shell-*` 临时文件。

提供任意命令执行意味着 Agent 获得该远端账号权限。应使用专用低权限账号，并通过操作系统/容器控制权限。上传下载可访问 MCP Server 进程权限允许的本地路径，请使用低权限本地账号或系统隔离保护私钥等敏感文件。添加服务器允许访问指定网络地址，部署方应通过网络策略限制可访问目标。

## 自动构建与发布

GitHub Actions 工作流位于 `.github/workflows/`：

- `ci.yml`：推送 `main` 或创建/更新 PR 时，在 Linux、macOS、Windows 上检查格式、验证依赖、构建、测试及运行 `go vet`；Linux 额外执行竞态检测。
- `release.yml`：推送严格的 `vMAJOR.MINOR.PATCH` 标签（如 `v0.1.0`）触发，先复用完整 CI，再交叉编译并创建 GitHub Release。Go 版本取自 `go.mod`，需为 `actions/setup-go` 可下载的版本。

发布步骤（先确保代码已提交并推送）：

```sh
git tag v0.1.0
git push origin v0.1.0
```

发布附件为独立二进制文件，不需要安装 Go：

| 系统 | amd64 | arm64 |
|---|---|---|
| Linux | `remote-shell-mcp-linux-amd64` | `remote-shell-mcp-linux-arm64` |
| macOS | `remote-shell-mcp-darwin-amd64` | `remote-shell-mcp-darwin-arm64` |
| Windows | `remote-shell-mcp-windows-amd64.exe` | `remote-shell-mcp-windows-arm64.exe` |

同时附带 `LICENSE` 和 `SHA256SUMS`。Linux 可用 `sha256sum --ignore-missing -c SHA256SUMS` 验证下载文件；macOS 可用 `shasum -a 256 <文件>`，Windows 可用 `Get-FileHash <文件> -Algorithm SHA256`，与校验文件对比。Linux/macOS 下载后需 `chmod +x <文件>`；仍需按前文准备连接配置。二进制未做代码签名或 macOS 公证。

构建使用 `CGO_ENABLED=0`；六种目标均交叉编译，但 CI 并未对每种架构都做实机运行验证。发布先创建草稿，附件上传成功后才公开；失败可重新运行工作流以恢复草稿发布。已公开版本不覆盖，请使用新版本标签；不要移动已经发布的标签。只有发布任务拥有仓库写权限，使用内置 `GITHUB_TOKEN`，不需另配令牌。

## 开源协议

本项目采用 [MIT License](LICENSE)，允许任何人使用、修改、分发及商用，须保留版权及许可声明。软件不提供任何担保。第三方依赖遵循各自的许可证。

## 测试与当前限制

```sh
go test ./...
go vet ./...
CGO_ENABLED=1 go test -race ./...
```

已提供配置持久化/锁、本地与远端路径解析、输出截断、MCP 工具发现与调用、内存 SSH 服务的认证/主机校验/执行结果测试，以及内存 SFTP 服务的原子写入、禁止覆盖、大小限制与临时文件清理测试。

尚需补充真实 OpenSSH/SFTP/tmux 端到端测试、取消与传输故障注入、大目录流式读取、各平台实机验证。跨平台编译成功不等同于运行验证。原子替换不构成跨进程文件比较交换，不保证阻止远端外部程序并发修改。
