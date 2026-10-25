package corecall_test

import (
	"strings"
	"testing"

	"github.com/ivannguyendev/chatim/pkg/corecall"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

func TestCommandsKeepTheOrderAndDescribeEachCall(t *testing.T) {
	methods := corecall.All()
	cmds, err := corecall.Commands(methods)
	if err != nil || len(cmds) != len(methods) {
		t.Fatalf("commands = %d, err = %v", len(cmds), err)
	}
	for i, c := range cmds {
		if c.Method != methods[i] || !strings.HasSuffix(c.Method, "/"+c.Name) {
			t.Fatalf("command %d = %s %s, want %s", i, c.Method, c.Name, methods[i])
		}
		if string(c.Request.Name()) != c.Name+"Request" || string(c.Response.Name()) != c.Name+"Response" {
			t.Fatalf("%s carries %s and %s", c.Name, c.Request.Name(), c.Response.Name())
		}
		if hasRoom := c.Request.Fields().ByName("room_id") != nil; hasRoom != (c.Room != nil) {
			t.Fatalf("%s room field = %v", c.Name, c.Room)
		}
	}
}

func TestCommandsRefuseUnknownAndRepeatedMethods(t *testing.T) {
	send := chatimv1.CoreService_SendMessage_FullMethodName
	for name, methods := range map[string][]string{
		"unknown":  {"/chatim.v1.CoreService/Nope"},
		"repeated": {send, send},
	} {
		if _, err := corecall.Commands(methods); err == nil {
			t.Fatalf("%s methods accepted", name)
		}
	}
}

func TestRoomIDReadsTheRoomField(t *testing.T) {
	cmds, err := corecall.Commands([]string{chatimv1.CoreService_SendMessage_FullMethodName, chatimv1.CoreService_ListMyRooms_FullMethodName})
	if err != nil {
		t.Fatal(err)
	}
	if room, ok := cmds[0].RoomID(&chatimv1.SendMessageRequest{RoomId: "42"}); !ok || room != "42" {
		t.Fatalf("room = %q %v", room, ok)
	}
	if _, ok := cmds[1].RoomID(&chatimv1.ListMyRoomsRequest{}); ok {
		t.Fatal("ListMyRooms has a room")
	}
}
