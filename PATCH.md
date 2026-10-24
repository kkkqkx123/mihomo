# 自定义变更说明

本仓库是 `MetaCubeX/mihomo` 的 fork，供 `mihomo-cli` 管理 Mihomo 内核进程。相对上游 `Alpha` 分支，本仓库只有一个功能性改动：新增 `/shutdown` REST API。同步上游或排查问题时，以本文列出的差异为准。

## 变更清单

| 文件 | 变更 |
| --- | --- |
| `hub/route/shutdown.go` | 新增，注册 `POST /shutdown` 并触发进程退出 |
| `hub/route/server.go` | 在非 embed 模式下挂载 `/shutdown`，与 `/restart` 同级 |

本文件 `PATCH.md` 仅用于记录差异，不参与编译。

## 实现说明

`POST /shutdown` 的处理流程：

1. 先返回 `{"status":"ok"}` 并 flush。进程随后就会退出，必须先让调用方拿到完整响应。
2. 在独立 goroutine 中调用 `executor.Shutdown()`，它负责清理 listener、清理 tproxy 规则、持久化 fake-ip 状态，与 `main` 中 `defer executor.Shutdown()` 的工作一致。
3. 随后 `os.Exit(0)`。`os.Exit` 不会执行 defer，所以第 2 步必须显式调用；放在 goroutine 中则避免阻塞当前 HTTP 响应。

`/restart` 在 `executor.Shutdown()` 之后用 `syscall.Exec` 重启自身；`/shutdown` 需要真正结束进程，因此使用 `os.Exit(0)`。

该端点只在非 embed 模式下注册。embed 模式由宿主程序嵌入运行，不应允许 API 关闭宿主进程，因此与 `/restart` 保持一致的禁启用策略。

## 补丁内容

```diff
diff --git a/hub/route/server.go b/hub/route/server.go
index 6d37d480..94b0e1db 100644
--- a/hub/route/server.go
+++ b/hub/route/server.go
@@ -135,8 +135,9 @@ func router(isDebug bool, secret string, dohServer string, cors Cors) *chi.Mux {
 		r.Mount("/cache", cacheRouter())
 		r.Mount("/dns", dnsRouter())
 		r.Mount("/storage", storageRouter())
-		if !embedMode { // disallow restart in embed mode
+		if !embedMode { // disallow restart/shutdown in embed mode
 			r.Mount("/restart", restartRouter())
+			r.Mount("/shutdown", shutdownRouter())
 		}
 		r.Mount("/upgrade", upgradeRouter())
 		addExternalRouters(r)
diff --git a/hub/route/shutdown.go b/hub/route/shutdown.go
new file mode 100644
index 00000000..93ce2d9f
--- /dev/null
+++ b/hub/route/shutdown.go
@@ -0,0 +1,31 @@
+package route
+
+import (
+	"os"
+
+	"github.com/metacubex/mihomo/hub/executor"
+	"github.com/metacubex/mihomo/log"
+
+	"github.com/metacubex/chi"
+	"github.com/metacubex/chi/render"
+	"github.com/metacubex/http"
+)
+
+func shutdownRouter() http.Handler {
+	r := chi.NewRouter()
+	r.Post("/", shutdown)
+	return r
+}
+
+func shutdown(w http.ResponseWriter, r *http.Request) {
+	render.JSON(w, r, render.M{"status": "ok"})
+	if f, ok := w.(http.Flusher); ok {
+		f.Flush()
+	}
+
+	go func() {
+		log.Infoln("shutting down via API request")
+		executor.Shutdown()
+		os.Exit(0)
+	}()
+}
```

## 依赖方

`mihomo-cli` 的 `internal/api/shutdown.go` 调用该端点，daemon 停止流程优先使用 API 优雅退出，仅在 API 调用失败时才回退到强制结束进程。移除该端点会导致 CLI 只能强杀内核，丢失 tproxy 规则清理和 fake-ip 状态持久化。

## 上游同步

### 远端配置

upstream 的 fetch refspec 必须指向 `Alpha`：

```text
+refs/heads/Alpha:refs/remotes/upstream/Alpha
```

用 `git config remote.upstream.fetch` 检查。`MetaCubeX/mihomo` 的 `main` 分支与内核无关（是一份 Python 数据模型代码），若 refspec 误配为 `refs/heads/main`，任何一次 `git fetch upstream`（包括编辑器的自动 fetch）都会把 `upstream/Alpha` 覆盖成无关历史，表现为合并时出现大量莫名其妙的差异或冲突。

### 分支跟踪与同步基准

同步时让一个与上游同名的本地 `Alpha` 分支承担跟踪职责，`feat/add-shutdown-api` 只承载自定义提交，以免feat分支跟踪到错误的分支：

```text
upstream/Alpha ──快进──▶ 本地 Alpha ──合并──▶ feat/add-shutdown-api
```

本地 `Alpha` 始终与上游保持一致，不在其上提交自定义改动。

### 同步步骤

1. 带代理拉取上游：`export https_proxy="http://127.0.0.1:7890"; git fetch --no-tags upstream`
2. 在 Alpha 分支上更新到上游（首次需要创建本地分支）：

   ```bash
   git switch --track -c Alpha upstream/Alpha   # 首次
   git switch Alpha
   git merge --ff-only upstream/Alpha           # 之后每次
   ```

   若本地 Alpha 已存在，先用 `git rev-parse Alpha upstream/Alpha` 确认两者一致；不一致时（例如曾经跟踪过 fork 的 Alpha，或误在其上提交过）用 `git reset --hard upstream/Alpha` 强制对齐，否则后续合并会把无关的旧历史带回 feat 分支。

3. 切回 feat 分支合并 Alpha：

   ```bash
   git switch feat/add-shutdown-api
   git merge-tree --write-tree HEAD Alpha   # 预检冲突，退出码为 0 即为无冲突
   git merge Alpha
   ```

   需要线性历史时用 `git rebase Alpha` 代替 `git merge Alpha`，基准相同，只是不产生合并提交。

4. 核对：`git log --oneline Alpha..HEAD` 应只列出本文对应的自定义提交，`git diff Alpha HEAD` 应只包含本文列出的文件。若上游改动了同名文件产生冲突，直接按本文补丁重新应用。
5. 验证：`go build ./hub/route && go vet ./hub/route`。

## 历史整理记录

上游基线为 `Alpha@91e85eeb`（2026-10-24）。原 `feat/add-shutdown-api` 分支建立在旧的上游合并历史之上，功能提交为 `01a0fcbe`，夹杂两次上游合并提交。整理时 reset 到上述基线并重建为单个提交 `fe0db91d`，文件内容与整理前逐字节一致，`go build ./hub/route` 与 `go vet ./hub/route` 均通过。
