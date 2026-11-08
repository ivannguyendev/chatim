package mediaapp

import chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"

func (a *App) Server() chatimv1.MediaServiceServer { return a.server }
