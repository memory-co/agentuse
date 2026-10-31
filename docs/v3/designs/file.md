# fileService：Session 文件空间

> v3 目标设计，尚未据此修改代码或存储。fileService 不使用数据库表。

## 职责

fileService 为每个 session 提供一个独立的文件空间，支持上传和下载。同一 session 下的用户与 agent 共享这些文件，不为不同 run 再复制一套。

文件的内容和存在性以存储后端为准，不建立 attachments 或 files 表，不记录上传人、来源 run、摘要、配额等数据库元数据。文件以 session_id 和相对路径定位，不另生成附件 ID。messages.attachments 保存这些相对路径的 JSON 数组，例如 `["inputs/data.csv","report.pdf"]`；run_prompts 可复制这份路径列表作为执行输入，fileService 本身仍不记录数据库。

## 两种存储形态

服务端配置选择一种后端，对调用方提供相同的上传、下载接口。

| 后端 | Session 文件空间 | 文件位置示例 |
|---|---|---|
| 本地文件系统 | `<root>/<session_id>/` | `/data/files/550e8400-e29b-41d4-a716-446655440000/report.pdf` |
| S3 协议的对象存储 | 指定 bucket 下的 `<prefix>/<session_id>/` | `sessions/550e8400-e29b-41d4-a716-446655440000/report.pdf` |

本地创建实际目录；S3 使用对象 key 的前缀表示目录，不需要专门创建目录对象。S3 配置包含 endpoint、bucket、凭据和可选 prefix；本地配置只需 root。凭据由 server 持有，client 不需要区分后端。

## 上传和下载

| 操作 | 行为 |
|---|---|
| 上传 | 指定 session 和文件相对路径，接收文件流，写入对应目录或对象 key |
| 下载 | 按相同 session 和路径读取文件，以流返回；不存在则返回 404 |

建议接口：

```text
PUT /api/sessions/{session_id}/files/{path...}
GET /api/sessions/{session_id}/files/{path...}
```

例如，向 `550e8400-e29b-41d4-a716-446655440000` 上传 `report.pdf` 后，用同一路径下载。重复上传相同路径覆盖原文件；本地通过临时文件写完后替换，S3 完成对象上传后生效，失败不把半个文件当作成功结果。

允许 session 目录中的相对子路径，例如 `inputs/data.csv`。路径必须限制在当前 session 文件空间内，不接受绝对路径、`..` 或通过符号链接越出目录。

## 与其他 Service 的关系

- controller 接收 HTTP 文件流、返回下载响应。
- sessionService 校验 session 存在及当前身份的访问权，然后交给 fileService 操作文件。
- client 执行 run 时，通过相同接口上传和下载该 session 的文件；设备或工作令牌的访问范围分别由 runService、workService 校验。
- session 删除后，由 sessionService 调用 fileService 清理对应目录或对象前缀。文件操作不加入 SQLite 事务，不能把数据库删除当作存储清理完成。

fileService 只处理 session 文件空间；文件如何用于 prompt、怎样交给 harness，由执行端负责。

## 表结构

无。文件直接存本地目录或 S3 对象存储，不新增文件数据库表。

## 相关设计

[Server 总览](server.md) · [sessionService](session.md) · [runService](run.md) · [workService](work.md)
