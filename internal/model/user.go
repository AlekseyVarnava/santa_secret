package model

type User struct {
	ID      int64
	GroupID int64
	TgID    int64
	Active  bool
	Desires string
	Leader  bool
}
