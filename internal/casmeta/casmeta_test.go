package casmeta

import (
	"bytes"
	"math/rand"
	"strings"
	"testing"

	"github.com/OpenListTeam/OpenList/v4/pkg/torrent"
)

// 分片大小一旦和上游/云端不一致，.cas 就会“生成成功但永远秒传失败”，
// 因此这两条是最重要的回归网。
func TestDefaultSliceSizeMatchesTorrent(t *testing.T) {
	if DefaultSliceSize != torrent.DefaultPieceSize {
		t.Fatalf("casmeta.DefaultSliceSize=%d, torrent.DefaultPieceSize=%d", DefaultSliceSize, torrent.DefaultPieceSize)
	}
}

func TestSliceSizeForMatchesCASTable(t *testing.T) {
	sizes := []int64{
		0, 1,
		10 << 20,       // 恰为默认分片
		10<<20 + 1,     // 刚超过一片
		9990 << 20,     // 20MiB 档边界前
		9990<<20 + 1,   // 跨到 20MiB 档
		20000 << 20,    // 大于 1999 片上限
		100 << 30,      // 100GiB
		3 << 40,        // 3TiB
	}
	for _, size := range sizes {
		if got, want := SliceSizeFor(size), torrent.CASSliceSize(size); got != want {
			t.Fatalf("SliceSizeFor(%d)=%d, want %d", size, got, want)
		}
	}
}

// 旧版魔改分支写出的 .cas（只有 5 个字段）必须仍可读，
// 新版写出的 .cas 也必须能被上游 pkg/torrent 解析。
func TestEncodeDecodeCrossCompatibility(t *testing.T) {
	info := &Info{
		Name:     "movie.mkv",
		Size:     25 << 20,
		MD5:      strings.Repeat("A", 32),
		SliceMD5: strings.Repeat("B", 32),
	}
	data, err := Encode(info)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}

	// 1) casmeta 自己读回
	back, err := Decode(data)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if back.Name != info.Name || back.Size != info.Size || back.MD5 != info.MD5 || back.SliceMD5 != info.SliceMD5 {
		t.Fatalf("roundtrip mismatch: %+v", back)
	}

	// 2) 上游 pkg/torrent 也能读
	seed, err := torrent.DecodeCAS(data, torrent.DefaultParseLimits())
	if err != nil {
		t.Fatalf("torrent.DecodeCAS: %v", err)
	}
	if len(seed.Files) != 1 {
		t.Fatalf("expected 1 file, got %d", len(seed.Files))
	}
	if !strings.EqualFold(seed.Files[0].Hashes.MD5, info.MD5) {
		t.Fatalf("md5 mismatch: %q vs %q", seed.Files[0].Hashes.MD5, info.MD5)
	}
	if !strings.EqualFold(seed.Files[0].CASSliceMD5, info.SliceMD5) {
		t.Fatalf("sliceMd5 mismatch: %q vs %q", seed.Files[0].CASSliceMD5, info.SliceMD5)
	}

	// 3) 上游写出的 .cas（带扩展字段）casmeta 也能读
	up := torrent.NewSeed("movie.mkv", "test", torrent.DefaultPieceSize)
	up.Files = []torrent.SeedFile{{
		Path:        "movie.mkv",
		Size:        info.Size,
		Hashes:      torrent.SeedHashes{MD5: info.MD5},
		CASSliceMD5: info.SliceMD5,
	}}
	upData, err := torrent.EncodeCAS(up)
	if err != nil {
		t.Fatalf("torrent.EncodeCAS: %v", err)
	}
	upBack, err := Decode(upData)
	if err != nil {
		t.Fatalf("Decode upstream payload: %v", err)
	}
	if upBack.MD5 != info.MD5 || upBack.SliceMD5 != info.SliceMD5 || upBack.Size != info.Size {
		t.Fatalf("upstream payload mismatch: %+v", upBack)
	}

	// 4) 原始 JSON（不带 base64）也要兼容
	raw, err := Decode([]byte(`{"name":"a.bin","size":1,"md5":"` + strings.Repeat("C", 32) + `","sliceMd5":"` + strings.Repeat("C", 32) + `"}`))
	if err != nil {
		t.Fatalf("Decode raw json: %v", err)
	}
	if raw.Name != "a.bin" || raw.Size != 1 {
		t.Fatalf("raw json mismatch: %+v", raw)
	}
}

// 一次遍历算出的 md5/sliceMd5 必须与上游 HashWriter 逐字节一致。
func TestHasherWriterMatchesTorrentHashWriter(t *testing.T) {
	lengths := []int64{5 << 20, 10 << 20, 10<<20 + 1, 25 << 20}
	for _, n := range lengths {
		buf := make([]byte, n)
		if _, err := rand.New(rand.NewSource(n)).Read(buf); err != nil {
			t.Fatalf("rand: %v", err)
		}

		w := NewHasherWriter(SliceSizeFor(n))
		if _, err := w.Write(buf); err != nil {
			t.Fatalf("write: %v", err)
		}
		info := w.Info("f.bin")

		hw := torrent.NewHashWriter(torrent.CASSliceSize(n), torrent.CASSliceSize(n), n)
		if _, err := hw.Write(buf); err != nil {
			t.Fatalf("hashwriter write: %v", err)
		}
		hw.Finish()
		fileMD5 := hw.GetFileMD5()

		if !strings.EqualFold(info.MD5, fileMD5) {
			t.Fatalf("size=%d fileMD5 mismatch: %q vs %q", n, info.MD5, fileMD5)
		}
		wantSlice := hw.GetSliceMD5(fileMD5)
		if wantSlice == "" {
			wantSlice = fileMD5
		}
		if !strings.EqualFold(info.SliceMD5, wantSlice) {
			t.Fatalf("size=%d sliceMD5 mismatch: %q vs %q", n, info.SliceMD5, wantSlice)
		}
		if info.Size != n {
			t.Fatalf("size=%d written=%d", n, info.Size)
		}
	}
}

func TestHasherWriterSplitsAcrossWrites(t *testing.T) {
	// 分片边界落在多次 Write 之间，结果必须与一次性写入一致
	data := bytes.Repeat([]byte("0123456789"), 2<<20) // 20MiB
	once := NewHasherWriter(SliceSizeFor(int64(len(data))))
	if _, err := once.Write(data); err != nil {
		t.Fatal(err)
	}
	want := once.Info("f.bin")

	step := NewHasherWriter(SliceSizeFor(int64(len(data))))
	for i := 0; i < len(data); i += 777777 {
		end := min(i+777777, len(data))
		if _, err := step.Write(data[i:end]); err != nil {
			t.Fatal(err)
		}
	}
	got := step.Info("f.bin")
	if got.MD5 != want.MD5 || got.SliceMD5 != want.SliceMD5 || got.Size != want.Size {
		t.Fatalf("chunked hashing mismatch: %+v vs %+v", got, want)
	}
}
