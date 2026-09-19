package room

import "math"

// onlineUsersLocked counts distinct signed-in viewers.
func (r *Room) onlineUsersLocked() int {
	seen := map[string]struct{}{}
	for _, v := range r.viewers {
		if v.user != nil {
			seen[v.user.Username] = struct{}{}
		}
	}
	return len(seen)
}

// skipNeededLocked is how many skip votes end the current item in vote
// mode: ceil(online users × threshold), at least one.
func (r *Room) skipNeededLocked() int {
	if !r.info.Settings.VoteMode {
		return 0
	}
	n := int(math.Ceil(float64(r.onlineUsersLocked()) * r.info.Settings.SkipThreshold))
	if n < 1 {
		n = 1
	}
	return n
}
