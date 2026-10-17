package domain

type RoomType uint8

const (
	RoomDM      RoomType = 1
	RoomGroup   RoomType = 2
	RoomChannel RoomType = 3
)
