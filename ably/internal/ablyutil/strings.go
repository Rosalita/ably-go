package ablyutil

import (
	"math/rand"
	"strings"
	"time"
)

func Shuffle(l []string) []string {
	src := rand.NewSource(time.Now().UnixNano())
	r := rand.New(src)
	for i := range l {
		n := r.Intn(len(l) - 1)
		l[i], l[n] = l[n], l[i]
	}
	return l
}

func Contains(s []string, str string) bool {
	for _, v := range s {
		if v == str {
			return true
		}
	}
	return false
}

func Empty(s string) bool {
	return len(strings.TrimSpace(s)) == 0
}