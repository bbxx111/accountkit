# Spec Delta

## MODIFIED Requirements

### Requirement: 批量公开账号资料
库 SHALL 提供内部 Go 批量资料查询，仅返回账号 ID、显示名、头像地址和生命周期状态，不含联系方式、身份、会话和密钥，不新增任意用户公开 HTTP 查询。输入 SHALL 去重；空输入返回空结果，格式错误 ID 返回参数错误，合法未知 ID 不在结果中。PENDING_DELETION/DELETED 账号 SHALL 返回空显示名和空头像地址。

#### Scenario: 一页一次资料查询
- **WHEN** 宿主传入授权业务页涉及的重复账号 ID
- **THEN** 通过一次批量查询返回去重资料，不逐个查询账号或身份

#### Scenario: 未知和注销资料
- **WHEN** 输入包含未知、待注销或已匿名化账号
- **THEN** 未知账号不返回，注销账号不返回原显示名及头像，由宿主决定匿名文案
