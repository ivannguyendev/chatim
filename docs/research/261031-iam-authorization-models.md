# Mô hình phân quyền cho `iam`: chuẩn và cách Keycloak, Zitadel làm

> Ngày: 2026-10-31 · Phục vụ M6 phần 3c (role, permission, API admin) · Phạm vi: chuẩn IAM. Role theo room, thành viên room là việc của core (D179), không thuộc tài liệu này.
> Nguồn: tài liệu chính thức đọc ngày 2026-10-31. Chỗ ghi "chưa thấy nguồn" là không tìm thấy trong tài liệu đã đọc, không có nghĩa là không tồn tại.

## 1. Kết luận

`iam` dùng **RBAC theo tenant, gán role qua nhóm, nhóm con thừa hưởng** (kiểu Keycloak, Google Cloud), và **luật quản trị kiểu ARBAC**: quyền quản trị cũng là permission; ai cấp gì cũng chỉ được cấp phần mình có (luật chống leo quyền của Kubernetes). Role dựng sẵn `tenant-admin` là ngoại lệ duy nhất, có mọi quyền, giống `cluster-admin` của Kubernetes hay Owner của Google Cloud.

**Không phải việc của `iam`:**
- **Quyền theo từng tài nguyên** (ReBAC, kiểu Google Zanzibar, OpenFGA). Ví dụ "Lan là owner của room 42": việc của app; với chatim là `members` của core.
- **Quyết định theo thuộc tính** (ABAC). Ví dụ "chỉ trong giờ hành chính": việc của app.
- **Đánh giá policy của app** (D217). `iam` chỉ lưu và cấp permission.

## 2. Các mô hình chuẩn

| Mô hình | Ý chính | Khi nào cần | Với `iam` |
|---|---|---|---|
| **RBAC** (NIST, ANSI INCITS 359-2004) | Người → role → permission. Bốn tầng: lõi, cây role, tách nhiệm vụ tĩnh, tách nhiệm vụ động | Quyền theo chức vụ trong tổ chức | **Dùng** (lõi). Không dùng cây role và tách nhiệm vụ: chưa có yêu cầu |
| **ARBAC97/02** (Sandhu) | Quản trị RBAC bằng RBAC: `can_assign`, `can_revoke`; role quản trị tách khỏi role thường | Giao việc quản trị cho người khác | **Dùng** ý chính, theo cách làm của Kubernetes |
| **ABAC** (NIST SP 800-162) | Quyết định theo thuộc tính người, tài nguyên, môi trường | Luật theo giờ, nơi, mức mật | Không, thuộc app |
| **ReBAC** (Zanzibar; OpenFGA, SpiceDB) | Bộ ba (đối tượng, quan hệ, người) | Chia sẻ theo từng đối tượng | Không, thuộc app |

Nguồn: [NIST RBAC FAQ](https://csrc.nist.gov/projects/role-based-access-control/faqs), [NIST 2000](https://csrc.nist.gov/pubs/conference/2000/07/26/nist-model-for-rbac-towards-a-unified-standard/final), [ARBAC97](https://profsandhu.com/confrnc/rbac/arbac97.pdf), [NIST SP 800-162](https://csrc.nist.gov/pubs/sp/800/162/upd2/final), [Zanzibar](https://research.google/pubs/zanzibar-googles-consistent-global-authorization-system/), [OpenFGA](https://openfga.dev/docs/learn/zanzibar.md).

**Về nhóm:** chuẩn RBAC không có "nhóm". Gán role cho nhóm nghĩa là gán role cho mọi người trong nhóm. Nhóm con thừa hưởng role của nhóm cha là cách Keycloak ("Subgroups inherit the attributes and role mappings from their parent") và Google Cloud (policy đặt ở org/folder đi xuống project) làm. Cây nhóm khác cây role. `iam` có cây nhóm, không có cây role.

**Về chữ "session":** trong NIST, session là tập role được bật trong một lần làm việc. "Phiên" của `iam` là phiên đăng nhập. Hai khái niệm khác nhau; tài liệu của `iam` dùng "phiên" theo nghĩa đăng nhập.

## 3. Giao việc quản trị an toàn

| Ở đâu | Cách làm | Nguồn |
|---|---|---|
| Kubernetes | Tạo/sửa role chỉ khi đã có mọi quyền trong role đó; gán role (binding) chỉ khi đã có quyền của role. Ngoại lệ phải cấp rõ: `escalate`, `bind`. Chỉ có cho phép, không có cấm | [K8s RBAC](https://kubernetes.io/docs/reference/access-authn-authz/rbac/) |
| Keycloak | Admin có `manage-users` chỉ gán được role quản trị mà mình đang có. Quyền chia nhỏ: xem, sửa, gán role (`map-roles`), đặt lại mật khẩu, quản lý thành viên nhóm. Có quyền "sửa" không tự có quyền "xem". Admin được giao không gán được role quản trị cho admin khác | [Keycloak admin guide](https://www.keycloak.org/docs/latest/server_admin/index.html) |
| Zitadel | Role quản trị cố định theo tầng: instance (`IAM_OWNER`…), org (`ORG_OWNER`, `ORG_USER_MANAGER`…), project (`PROJECT_OWNER`…). Người tạo org thành `ORG_OWNER` | [Zitadel administrators](https://zitadel.com/docs/guides/manage/console/administrators) |
| AWS | "Permission boundary": trần quyền người được giao không vượt qua và không sửa được | [AWS boundaries](https://docs.aws.amazon.com/IAM/latest/UserGuide/access_policies_boundaries.html) |
| Google Cloud | Quyền cộng dồn từ trên xuống cây tài nguyên; "cấm" là loại policy riêng | [GCP resource hierarchy](https://docs.cloud.google.com/iam/docs/resource-hierarchy-access-control) |

**Luật chung rút ra:**
1. Quyền quản trị cũng là permission, không phải trường hợp đặc biệt.
2. Ai cấp gì cũng chỉ được cấp phần mình có. Ngoại lệ phải ít và ghi rõ.
3. Quyền chia nhỏ và độc lập: quản lý account, gán role, đặt lại mật khẩu, quản lý thành viên nhóm là các quyền khác nhau.
4. Chỉ có cho phép, cộng dồn theo cây; không có luật cấm.

## 4. Keycloak và Zitadel cạnh nhau

| Khái niệm | Keycloak | Zitadel | `iam` (đề xuất) |
|---|---|---|---|
| Tenant | Realm | Organization (trong instance) | Tenant |
| Quản trị toàn hệ thống | Admin của realm `master` | `IAM_OWNER` | CLI vận hành (tenant, khoá ký, migrate) |
| Người | Theo realm | Theo org, gồm cả account máy | Account theo tenant |
| Nhóm | Cây; nhóm con thừa hưởng role | Chỉ để gom trên giao diện | Cây; nhóm con thừa hưởng role |
| Nơi đặt tên role | Role của realm, role của client | Role của project | Role theo tenant, permission mang tên client |
| Role gộp role | Có (composite) | Chưa thấy nguồn | Không (chưa cần) |
| Quyền quản trị | Quyền chia nhỏ theo loại tài nguyên | Role quản trị cố định | Permission `iam:*` chia nhỏ |
| Role trong token | Có (mặc định) | Có, claim `urn:zitadel:iam:org:project:roles` | Không; đọc DB mỗi lần gọi (D233) |
| Account máy | Service account của client, đăng nhập bằng `client_credentials` | Account máy: khoá riêng ký JWT (khuyên dùng), secret, PAT | Xem câu hỏi §7 |
| Luôn còn owner | Chưa thấy nguồn | Chưa thấy nguồn | Không ép (owner 2026-10-31); mất admin cuối thì cứu bằng CLI |

## 5. App nhận quyền từ IAM thế nào

| Cách | Mới tới đâu | Chi phí | Nguồn |
|---|---|---|---|
| Role/permission trong JWT | Cũ tới khi token hết hạn | Không gọi thêm; token to dần theo số role | [RFC 9068](https://www.rfc-editor.org/rfc/rfc9068): `roles`, `groups`, `entitlements` là tuỳ chọn |
| Hỏi lại `iam` mỗi lần (introspection RFC 7662, AuthZEN) | Luôn mới | Thêm một lần gọi mạng | [AuthZEN 1.0](https://openid.net/specs/authorization-api-1_0.html), chuẩn cuối duyệt 2026-01-12 |
| Bản sao đổi theo tín hiệu | Mới sau vài ms | Một luồng nghe thay đổi | Feed 3a của `iam` (D227) |

Keycloak và Zitadel mặc định cho role vào token. `iam` cố ý không làm vậy (D233), để gỡ quyền có hiệu lực ngay; hợp chuẩn vì RFC 9068 để các claim này tuỳ chọn. Cách app đọc permission chọn lúc tích hợp sau khi `iam` xong (D234).

## 6. Luật `iam` phải giữ

| Luật | Tương ứng trong chuẩn |
|---|---|
| Sửa role, gán role, thêm người vào nhóm, đổi nhóm cha: chỉ trao phần mình có | Kubernetes `escalate`/`bind`; Keycloak `manage-users` |
| Tác động lên người khác (đặt lại mật khẩu, khoá, thu hồi phiên) chỉ khi mình có ít nhất bằng quyền người đó; chỉ `tenant-admin` tác động lên `tenant-admin` | Keycloak: admin được giao không đụng được admin khác |
| Quyền chia nhỏ, độc lập, không có `*` | Keycloak quyền chia nhỏ; permission của GCP không có wildcard |
| Permission mang tên client: `<client>:<resource>.<action>` | Role theo client (Keycloak), project (Zitadel); `storage.objects.get` (GCP) |
| `tenant-admin` là ngoại lệ duy nhất, có mọi quyền; chỉ `tenant-admin` gán `tenant-admin` | `cluster-admin` (K8s), Owner (GCP); ARBAC |
| Không ép tenant luôn còn admin; mất admin cuối thì cứu bằng CLI | GitHub chỉ **khuyên** ≥ 2 owner ([GitHub](https://docs.github.com/en/organizations/managing-peoples-access-to-your-organization-with-roles/maintaining-ownership-continuity-for-your-organization)); Keycloak, Zitadel không có luật này. Owner bỏ luật cứng 2026-10-31 |
| Mọi lệnh chỉ trong tenant của người gọi | Cô lập tenant (realm, org) |
| Mọi thay đổi ghi audit kèm người làm | Thực hành chung |
| Account `tenant-admin` chỉ dùng cho việc quản trị cấp cao; việc hằng ngày dùng role khác | Google: super admin là account riêng cho lúc khẩn cấp ([GCP](https://docs.cloud.google.com/resource-manager/docs/super-admin-best-practices)). Ghi trong hướng dẫn vận hành, không ép bằng code |

## 7. Bản nháp 3c lệch chuẩn ở đâu

| Chỗ lệch | Mức | Đề xuất |
|---|---|---|
| Backend gọi API admin bằng account **mật khẩu** (`svc-acme`) | Lệch: cả hai sản phẩm coi account máy là loại riêng, đăng nhập bằng khoá hoặc secret qua `client_credentials` | Câu hỏi cho owner (xem báo cáo) |
| Permission là chuỗi tự do, `chatim:*` gõ sai vẫn lưu, cấp mà không có tác dụng | Lệch nhẹ: GCP, AWS có danh mục permission mỗi dịch vụ | Mỗi client đăng ký danh mục permission; sửa role thì kiểm theo danh mục |
| Quyền quản trị chưa chia nhỏ (gom "sửa account") | Lệch: Keycloak tách quản lý, gán role, đặt lại mật khẩu, thành viên nhóm | Danh mục `iam:` chia nhỏ (§6) |
| Không cho role vào token | Khác mặc định của cả hai, nhưng hợp chuẩn | Giữ (D233); ghi rõ lý do |
| Luật "luôn còn owner" | Chặt hơn cả hai | Owner bỏ 2026-10-31; cứu bằng CLI |
| Không có cây role, không tách nhiệm vụ | Đúng (chưa có yêu cầu) | Giữ |
