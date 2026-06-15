package app

import (
	"sync"
	"time"
)

type ProbeBucket struct {
	Minute   int64 `json:"-"`
	Label    string `json:"t"`
	Online   int    `json:"online"`
	Healthy  int    `json:"healthy"`
	Total    int    `json:"total"`
	MaxLatMs int64  `json:"maxLatMs"`
}

type probeHistory struct {
	mu      sync.Mutex
	buckets map[string]map[int64]*ProbeBucket // group -> minute_ts -> bucket
}

func newProbeHistory() *probeHistory {
	return &probeHistory{buckets: map[string]map[int64]*ProbeBucket{}}
}

func (ph *probeHistory) Record(group string, online int, ok bool, latencyMs int64) {
	now := time.Now().Truncate(time.Minute).Unix()
	ph.mu.Lock()
	defer ph.mu.Unlock()
	gm := ph.buckets[group]
	if gm == nil {
		gm = map[int64]*ProbeBucket{}
		ph.buckets[group] = gm
	}
	b := gm[now]
	if b == nil {
		b = &ProbeBucket{Minute: now, Label: time.Unix(now, 0).Format("15:04"), Online: online}
		gm[now] = b
	}
	if online > b.Online {
		b.Online = online
	}
	b.Total++
	if ok {
		b.Healthy++
	}
	if latencyMs > b.MaxLatMs {
		b.MaxLatMs = latencyMs
	}
}

func (ph *probeHistory) RecordOnline(group string, online int) {
	now := time.Now().Truncate(time.Minute).Unix()
	ph.mu.Lock()
	defer ph.mu.Unlock()
	gm := ph.buckets[group]
	if gm == nil {
		gm = map[int64]*ProbeBucket{}
		ph.buckets[group] = gm
	}
	b := gm[now]
	if b == nil {
		b = &ProbeBucket{Minute: now, Label: time.Unix(now, 0).Format("15:04")}
		gm[now] = b
	}
	if online > b.Online {
		b.Online = online
	}
}

func (ph *probeHistory) Last60(group string) []ProbeBucket {
	end := time.Now().Truncate(time.Minute).Unix()
	ph.mu.Lock()
	gm := ph.buckets[group]
	out := make([]ProbeBucket, 60)
	for i := 59; i >= 0; i-- {
		ts := end - int64(i)*60
		if b, ok := gm[ts]; ok {
			out[59-i] = *b
		} else {
			out[59-i] = ProbeBucket{Minute: ts, Label: time.Unix(ts, 0).Format("15:04")}
		}
	}
	ph.mu.Unlock()
	return out
}

func (ph *probeHistory) Cleanup() {
	cutoff := time.Now().Add(-65 * time.Minute).Truncate(time.Minute).Unix()
	ph.mu.Lock()
	defer ph.mu.Unlock()
	for group, gm := range ph.buckets {
		for ts := range gm {
			if ts < cutoff {
				delete(gm, ts)
			}
		}
		if len(gm) == 0 {
			delete(ph.buckets, group)
		}
	}
}
