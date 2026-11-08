# 对话文件与消息附件

每个 conversation 都有独立的文件树。桌面端在对话右侧显示文件栏，小屏幕通过“文件”按钮打开抽屉；文件栏支持多级目录浏览、搜索、图片和文本预览、下载，以及把已有文件添加到下一条消息。

```text
/
├── user_input/
│   ├── 截图.png
│   └── specs/需求.txt
└── reports/
    └── daily/result.txt
```

用户上传的文件进入 `user_input/`。AI 产物使用自己的相对路径，例如 `reports/daily/result.txt`。相同路径的重复上传会生成带编号的新路径，保留原文件。不同 conversation 的路径相互独立。

## 随消息发送文件

1. 在消息输入框点击回形针按钮，选择本条消息的文件。发送前可以预览和移除，也可以只发文件。
2. 点击发送后，页面先上传尚未保存的文件，再提交本条消息选定的附件。
3. 消息下方显示文件卡片及相对路径，点击即可在右侧预览。下一条消息默认不携带之前的附件。
4. 复用已上传文件时，在右侧选中文件，点击“添加到消息”。

新会话和后续消息使用相同流程。上传或发送失败时保留草稿和已成功上传的文件，重试会复用这些文件。已经被消息引用的附件会保留到对话删除时一起清理；未关联消息的文件可以从文件栏删除。

Client 只接收本条消息明确选定的附件。一个 run 受理多条补充消息时，`context attachment list` 会合并这些消息的输入，但不会自动列入整个 conversation 的其他文件。

## AI 输出

AI 使用 `context attachment upload` 上传产物。在 workspace 内上传 `reports/result.txt` 时，会保留这个相对路径；workspace 外的文件使用文件名。`user_input/` 专用于用户输入，AI 无法写入该目录。

上传后文件出现在对应 conversation 的文件树中；run 的最终回复会附上本次产物的文件卡片。上下文工具说明见 [Client context](../client/context.md)。

## API

上传仍使用 `POST /api/sessions/{id}/attachments`，multipart 可提供 `path` 指定相对路径；用户路径始终位于 `user_input/` 下。Attachment 返回 `relative_path`。

消息通过 `POST /api/sessions/{id}/messages` 的 `attachment_ids` 选择文件：

```json
{
  "prompt": "请分析这张图片",
  "to_agent_ids": ["image-helper"],
  "attachment_ids": ["at_示例"]
}
```

省略或空数组表示本条消息没有附件。附件必须属于当前 conversation，最多 64 个；重复 ID 去重。没有文本时可以只提供附件。Message 的 `attachments` 保存所选文件的元数据快照，最终 AI 回复同样记录该 run 的输出文件。

当前使用数据库 schema 6。不提供旧数据迁移或兼容读取；升级时按项目约定重建数据库，两端一起更新。

目录创建、移动、重命名等后续操作列在 [文件树设计的 TODO](../../designs/v3/designs/conversation-files.md#todo)。
