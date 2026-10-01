package msgtext

import (
	"fmt"
	"math/rand/v2"
	"os"
	"strings"
)

var words = strings.Fields(`xin chào shop ạ mình muốn hỏi đơn hàng này giao khi nào vậy
cảm ơn bạn nhiều nhé sản phẩm kem chống nắng son dưỡng môi sữa rửa mặt còn hàng không
giá bao nhiêu có khuyến mãi freeship đổi trả được không địa chỉ số điện thoại mã giảm giá
hôm nay ngày mai buổi sáng chiều tối mình đã chuyển khoản rồi kiểm tra giúp mình với
okay dạ vâng được ạ chị em anh bên mình sẽ liên hệ lại sớm nhất có thể`)

func Synthetic(rng *rand.Rand) string {
	parts := make([]string, 20+rng.IntN(40))
	for i := range parts {
		parts[i] = words[rng.IntN(len(words))]
	}
	return strings.Join(parts, " ")
}

func Pick(texts []string, rng *rand.Rand) string {
	if len(texts) == 0 {
		return Synthetic(rng)
	}
	return texts[rng.IntN(len(texts))]
}

func Sender(rng *rand.Rand) string {
	return "user-" + string(rune('a'+rng.IntN(26)))
}

func Load(path string) ([]string, error) {
	if path == "" {
		return nil, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read text file: %w", err)
	}
	var texts []string
	for line := range strings.SplitSeq(string(data), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			texts = append(texts, line)
		}
	}
	return texts, nil
}
