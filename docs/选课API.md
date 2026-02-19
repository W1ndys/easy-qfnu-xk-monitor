根据代码分析，我提取出了五个选课搜索模块的详细信息：

---

## 五个选课搜索模块

| 模块标识      | 中文名称         | 搜索URL        | 选课操作      |
| ------------- | ---------------- | -------------- | ------------- |
| `xsxkKnjxk`   | 专业内跨年级选课 | `/xsxkKnjxk`   | `knjxkOper`   |
| `xsxkBxqjhxk` | 本学期计划选课   | `/xsxkBxqjhxk` | `bxqjhxkOper` |
| `xsxkXxxk`    | 选修选课         | `/xsxkXxxk`    | `xxxkOper`    |
| `xsxkFawxk`   | 计划外选课       | `/xsxkFawxk`   | `fawxkOper`   |
| `xsxkGgxxkxk` | 公选课选课       | `/xsxkGgxxkxk` | `ggxxkxkOper` |

---

## 1. 课程搜索 API

### URL

```
POST http://zhjw.qfnu.edu.cn/jsxsd/xsxkkc/{moduleType}
```

### 请求参数

**Query 参数：**
| 参数名 | 类型 | 必填 | 说明 |
|-------|------|------|------|
| `kcxx` | string | 是 | 课程号 |
| `skls` | string | 是 | 授课教师 |
| `sfym` | string | 是 | 是否过滤已满 (`false`) |
| `sfct` | string | 是 | 是否过滤冲突 (`false`) |
| `sfxx` | string | 是 | 是否过滤限选 (`false`) |
| `skxq` | string | 否 | 上课星期 |
| `skjc` | string | 否 | 上课节次 |

**Body 参数 (application/x-www-form-urlencoded)：**
| 参数名 | 类型 | 说明 |
|-------|------|------|
| `iDisplayStart` | string | 起始位置 (`0`) |
| `iDisplayLength` | string | 返回数量 (`10000`) |

---

## 2. 选课操作 API

### URL

```
GET http://zhjw.qfnu.edu.cn/jsxsd/xsxkkc/{operAction}
```

### 请求参数

**Query 参数：**
| 参数名 | 类型 | 说明 |
|-------|------|------|
| `kcid` | string | 课程ID (`JX02ID`) |
| `jx0404id` | string | 教学班ID (`JX0404ID`) |
| `_` | string | 时间戳 (毫秒) |

**请求头：**
| 头字段 | 值 |
|-------|-----|
| `Referer` | `http://zhjw.qfnu.edu.cn/jsxsd/xsxkkc/{refererAction}` |

---

## 3. 搜索响应 JSON 格式

```json
{
  "aaData": [
    {
      "kch": "课程号",
      "kcmc": "课程名称",
      "skls": "授课教师",
      "syrs": "剩余人数",
      "jx0404id": "教学班ID",
      "jx02id": "课程ID",
      "jx0504id": "开课计划ID",
      "sksj": "上课时间",
      "xkrs": "选课人数",
      "pkrs": "排课人数",
      "dwmc": "开课单位",
      "ktmc": "课堂名称",
      "skdd": "上课地点",
      "zcxqjcList": [
        {
          "zc": "周次",
          "xq": "星期",
          "jc": "节次"
        }
      ]
    }
  ]
}
```

### 字段说明

| 字段         | 类型   | 说明               |
| ------------ | ------ | ------------------ |
| `kch`        | string | 课程号             |
| `kcmc`       | string | 课程名称           |
| `skls`       | string | 授课教师           |
| `syrs`       | string | 剩余人数           |
| `jx0404id`   | string | 教学班ID（选课用） |
| `jx02id`     | string | 课程ID（选课用）   |
| `jx0504id`   | int    | 开课计划ID         |
| `sksj`       | string | 上课时间描述       |
| `xkrs`       | int    | 已选人数           |
| `pkrs`       | int    | 排课人数           |
| `dwmc`       | string | 开课单位名称       |
| `ktmc`       | string | 课堂名称           |
| `skdd`       | string | 上课地点           |
| `zcxqjcList` | array  | 周次星期节次列表   |

---

## 4. 选课响应 JSON 格式

```json
{
  "success": true,
  "message": "选课成功",
  "jfViewStr": ""
}
```

### 字段说明

| 字段        | 类型        | 说明           |
| ----------- | ----------- | -------------- |
| `success`   | bool/string | 是否成功       |
| `message`   | string      | 响应消息       |
| `jfViewStr` | string      | 学分视图字符串 |

### 常见响应消息

| 消息                       | 含义                   |
| -------------------------- | ---------------------- |
| `选课成功`                 | 选课成功               |
| `当前教学班已选择`         | 已选该课程             |
| `此课堂选课人数已满`       | 课程已满（永久失败）   |
| `此课堂已设置选课限制`     | 选课限制（永久失败）   |
| `冲突`                     | 时间冲突（永久失败）   |
| `目前选课人数较多服务器忙` | 服务器繁忙（需要重试） |
