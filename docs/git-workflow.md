# Quy trình git — chatim

> Cập nhật: 2026-10-03. Áp dụng cho mọi milestone từ M2b trở đi.

## Tóm tắt

- `feat/*`, `fix/*`: việc đang làm. Sống ngắn, tạo từ `main`.
- `main`: mọi thứ đã xong và test xanh. **Chưa chắc go-live.**
- Tag `vX.Y.Z`: đánh dấu một commit trên `main` là một phiên bản release.
- prod: server chạy image build từ một tag. **Không phải nhánh.**

Không có nhánh `prod`, `develop` hay `release` thường trực.

```
feat/<ms>-<slug> ──PR──▶ main ──tag vX.Y.Z──▶ image chatim/core:vX.Y.Z (ghim digest)
                                                  ├─▶ staging / prod-like
                                                  └─▶ prod  (cùng image, không build lại)
```

## Nhánh và PR

- Tên nhánh: `feat/<milestone>-<slug>` (ví dụ `feat/m2b-edit-delete`), `fix/<slug>`, `docs/<slug>`.
- Một nhánh = một milestone, hoặc một lát milestone nếu lát đó tự đạt Definition of Done.
- Merge vào `main` qua PR, giữ merge commit. Xoá nhánh sau khi merge.
- Không commit thẳng `main`, không force-push `main`, không rewrite history của `main`.

## CI và bảo vệ nhánh

- `.github/workflows/ci.yml`, job `checks`: chạy `make fmt-check`, `make vet`, `make lint`, `make test` trên mọi PR vào `main` và mỗi lần push lên `main`. Go vẫn chạy trong container `golang:1.26` qua `make`, giống máy dev.
- CI chỉ chạy unit test. `make itest` và `make core-up && make e2e` cần hạ tầng nên vẫn chạy trên máy dev (DoD bước 2).
- Branch protection `main`: bắt buộc PR, bắt buộc status check `checks` xanh, không ai được bỏ qua.

## Definition of Done để merge vào main

1. Mọi task trong `docs/plans/<milestone>.md` đã xong; bước nào cho kết quả khác "Expected" đã được báo và xử lý.
2. Xanh: `make fmt-check`, `make vet`, `make lint`, `make test`, `make itest`, `make core-up && make e2e`.
3. Không còn finding Critical/Important; finding Minor ghi vào plan hoặc docs.
4. Cùng PR cập nhật: `docs/roadmap.md` (trạng thái và mức sẵn sàng), Decision Log trong design doc, `INDEXES.csv`, mục Done/Next trong `CLAUDE.md`.
5. Mô tả PR ghi mức sẵn sàng và những gì còn thiếu để go-live.

## Mức sẵn sàng

| Mức | Nghĩa |
|---|---|
| `dev-done` | Chạy đúng trên máy dev, qua DoD ở trên |
| `prod-like validated` | Đạt tiêu chí trên hạ tầng prod-like (PoC prod-like, load test) |
| `go-live` | Đã release bằng tag và chạy trên prod |

Merge vào `main` chỉ cần `dev-done`. Go-live là một quyết định riêng, đánh dấu bằng tag.

## Tag và release

- Tag theo **lần release**, không theo PR. Nhiều PR đã merge vào `main` được gộp vào một tag.
- SemVer `vMAJOR.MINOR.PATCH`, so với tag trước:
  - chỉ sửa lỗi → tăng PATCH (`v1.0.0` → `v1.0.1`);
  - có tính năng mới, vẫn tương thích → tăng MINOR (`v1.0.0` → `v1.1.0`);
  - phá tương thích (proto, API, format dữ liệu) → tăng MAJOR (`v2.0.0`).
- Trước go-live dùng `v0.x` (tuỳ chọn, ví dụ mỗi milestone `dev-done`). `v1.0.0` là lần go-live đầu tiên, sau M5 và PoC prod-like.
- Release notes: `gh release create vX.Y.Z --generate-notes` liệt kê các PR giữa hai tag.
- PR chưa đạt DoD thì chưa merge, nên không làm kẹt release. PR đã merge mà phát hiện lỗi trước khi tag thì sửa hoặc revert rồi mới tag.

Ví dụ: prod đang chạy `v1.0.0`; PR #10 (sửa tin), #11 (reaction), #12 (fix ghim) lần lượt merge vào `main`; prod vẫn chạy `v1.0.0`. Khi staging xác nhận, tag `v1.1.0` trên commit mới nhất của `main`, build image và deploy.

## Khi main bị lỗi

| Tình huống | Xử lý |
|---|---|
| `main` đỏ sau một lần merge | Fix-forward: `fix/<slug>` → PR → `main`, kèm test hồi quy. Không xong trong ~1 ngày hoặc chặn người khác → PR `git revert -m 1 <merge-commit>`, sửa trên nhánh feat rồi merge lại (khi merge lại phải revert commit revert) |
| Bug trên `main`, chưa release | `fix/<slug>` → PR → `main`; viết test tái hiện trước |
| Bug trên prod | Sửa trên `main`, tag patch `vX.Y.(Z+1)`. Nếu `main` đã có code chưa sẵn sàng → tạo `release/vX.Y` từ tag đang chạy, sửa và tag trên đó, cherry-pick fix về `main` |

Revert tạo commit mới nên không ảnh hưởng người đã pull `main`; reset và force-push thì có.
