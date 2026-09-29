package main

import (
	"fmt"
	"math/rand/v2"
	"os"
	"strings"
	"time"

	"github.com/ivannguyendev/chatim/pkg/keys"
)

type message struct {
	ID   []byte    `bson:"_id"`
	From string    `bson:"f"`
	Pts  int64     `bson:"p"`
	Kind int32     `bson:"kind"`
	Text string    `bson:"text"`
	TS   time.Time `bson:"ts"`
}

var words = strings.Fields(`xin chào shop ạ mình muốn hỏi đơn hàng này giao khi nào vậy
cảm ơn bạn nhiều nhé sản phẩm kem chống nắng son dưỡng môi sữa rửa mặt còn hàng không
giá bao nhiêu có khuyến mãi freeship đổi trả được không địa chỉ số điện thoại mã giảm giá
hôm nay ngày mai buổi sáng chiều tối mình đã chuyển khoản rồi kiểm tra giúp mình với
okay dạ vâng được ạ chị em anh bên mình sẽ liên hệ lại sớm nhất có thể`)

func newMessage(room, seq uint64, rng *rand.Rand, texts []string) message {
	text := ""
	if len(texts) > 0 {
		text = texts[rng.IntN(len(texts))]
	} else {
		parts := make([]string, 20+rng.IntN(40))
		for i := range parts {
			parts[i] = words[rng.IntN(len(words))]
		}
		text = strings.Join(parts, " ")
	}
	return message{
		ID:   keys.Msg(room, 0, seq),
		From: "user-" + string(rune('a'+rng.IntN(26))),
		Pts:  int64(seq),
		Kind: 1,
		Text: text,
		TS:   time.Now(),
	}
}

func loadTexts(path string) ([]string, error) {
	if path == "" {
		return nil, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read text file: %w", err)
	}
	var texts []string
	for _, line := range strings.Split(string(data), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			texts = append(texts, line)
		}
	}
	return texts, nil
}
