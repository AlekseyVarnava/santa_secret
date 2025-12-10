package service

import (
	"math/rand"
	"santa/internal/model"
	"time"
)

func ShuffleUsers(users []model.User) map[int64]int64 {
	rand.Seed(time.Now().UnixNano())

	shuffled := make([]model.User, len(users))
	copy(shuffled, users)
	rand.Shuffle(len(shuffled), func(i, j int) { shuffled[i], shuffled[j] = shuffled[j], shuffled[i] })

	result := make(map[int64]int64)
	for i, u := range users {
		result[u.TgID] = shuffled[i].TgID
	}
	return result
}
