package _189pc

import (
	"testing"

	"github.com/OpenListTeam/OpenList/v4/pkg/torrent"
)

const (
	mib = int64(1024 * 1024)
)

// 生成端（MyMediaHelper）用 1000 片上限，上游/本项目用 999，
// 只有边界窗口里两侧算出的分片大小不同。恢复必须能在窗口内换档命中。
func TestCASSliceSizeCandidates(t *testing.T) {
	tests := []struct {
		name string
		size int64
		want []int64
	}{
		{
			name: "tiny file stays single-slice",
			size: 1 << 20,
			want: []int64{10 * mib},
		},
		{
			name: "exactly 999 slices",
			size: 999 * 10 * mib,
			want: []int64{10 * mib},
		},
		{
			name: "first window: generator uses 10MiB, consumer computes 20MiB",
			size: 999*10*mib + 1,
			want: []int64{20 * mib, 10 * mib},
		},
		{
			name: "real sample from MyMediaHelper (wxsnr 1992)",
			size: 10478245501,
			want: []int64{20 * mib, 10 * mib},
		},
		{
			name: "exactly 1000 slices",
			size: 1000 * 10 * mib,
			want: []int64{20 * mib, 10 * mib},
		},
		{
			name: "just past 1000 slices",
			size: 1000*10*mib + 1,
			want: []int64{20 * mib, 10 * mib},
		},
		{
			name: "second window: 50MiB vs 20MiB",
			size: 20960000000,
			want: []int64{50 * mib, 20 * mib, 10 * mib},
		},
		{
			name: "huge file keeps a single candidate",
			size: 100 << 30,
			want: []int64{60 * mib},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := casSliceSizeCandidates(tt.size)
			if len(got) != len(tt.want) {
				t.Fatalf("size=%d got %v, want %v", tt.size, got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Fatalf("size=%d got %v, want %v", tt.size, got, tt.want)
				}
			}
		})
	}
}

// 候选必须覆盖上图片数上限：无论按 999 还是 1000 口径，标准档位都要在其中。
func TestCASSliceSizeCandidatesCoverBothLimits(t *testing.T) {
	for _, size := range []int64{
		1, 10 * mib, 999 * 10 * mib, 999*10*mib + 1, 1000 * 10 * mib,
		12 << 30, 20960000000, 30 << 30, 100 << 30,
	} {
		candidates := casSliceSizeCandidates(size)
		if len(candidates) == 0 {
			t.Fatalf("size=%d produced no candidate", size)
		}
		if candidates[0] != torrent.CASSliceSize(size) {
			t.Fatalf("size=%d standard candidate=%d, want %d", size, candidates[0], torrent.CASSliceSize(size))
		}
		alt := casSliceSizeByLimit(size, 1000)
		found := false
		for _, c := range candidates {
			if c == alt {
				found = true
				break
			}
		}
		if !found && alt != candidates[0] {
			t.Fatalf("size=%d missing generator candidate %d in %v", size, alt, candidates)
		}
		// 候选必须严格递减，便于逻辑上"降档重试"
		for i := 1; i < len(candidates); i++ {
			if candidates[i] >= candidates[i-1] {
				t.Fatalf("size=%d candidates not descending: %v", size, candidates)
			}
		}
	}
}

func TestIsPartSizeRejected(t *testing.T) {
	if isPartSizeRejected(nil) {
		t.Fatal("nil error must not be treated as part-size rejection")
	}
	if !isPartSizeRejected(&RespErr{Code: "InvalidPartSize"}) {
		t.Fatal("RespErr code InvalidPartSize should be detected")
	}
	if isPartSizeRejected(&RespErr{Code: "UserDayFlowOverLimited"}) {
		t.Fatal("day-flow limit must not be retried as part-size rejection")
	}
}
