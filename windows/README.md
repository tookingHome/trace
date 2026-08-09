# TRACE Windows

本目录为 **残影 TRACE** 的 Windows 客户端源码。项目说明、功能与发版方式见仓库根目录 [README.md](../README.md)。

## 构建

```powershell
$env:CGO_ENABLED = "0"
go build -trimpath -ldflags="-H windowsgui -s -w" -o publish\Trace.exe .
```

## 运行

```powershell
.\run.cmd
```

或直接运行 `publish\Trace.exe`。
