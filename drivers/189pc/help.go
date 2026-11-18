package _189pc

import (
	"bytes"
	"crypto/aes"
	"crypto/hmac"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha1"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"encoding/xml"
	"fmt"
	"math"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"github.com/OpenListTeam/OpenList/v4/pkg/torrent"
	"github.com/OpenListTeam/OpenList/v4/pkg/utils"
	"github.com/OpenListTeam/OpenList/v4/pkg/utils/random"
)

func clientSuffix() map[string]string {
	rand := random.Rand
	return map[string]string{
		"clientType": PC,
		"version":    VERSION,
		"channelId":  CHANNEL_ID,
		"rand":       fmt.Sprintf("%d_%d", rand.Int63n(1e5), rand.Int63n(1e10)),
	}
}

// 带params的SignatureOfHmac HMAC签名
func signatureOfHmac(sessionSecret, sessionKey, operate, fullUrl, dateOfGmt, param string) string {
	urlpath := regexp.MustCompile(`://[^/]+((/[^/\s?#]+)*)`).FindStringSubmatch(fullUrl)[1]
	mac := hmac.New(sha1.New, []byte(sessionSecret))
	data := fmt.Sprintf("SessionKey=%s&Operate=%s&RequestURI=%s&Date=%s", sessionKey, operate, urlpath, dateOfGmt)
	if param != "" {
		data += fmt.Sprintf("&params=%s", param)
	}
	mac.Write([]byte(data))
	return strings.ToUpper(hex.EncodeToString(mac.Sum(nil)))
}

// RAS 加密用户名密码
func RsaEncrypt(publicKey, origData string) string {
	block, _ := pem.Decode([]byte(publicKey))
	pubInterface, _ := x509.ParsePKIXPublicKey(block.Bytes)
	data, _ := rsa.EncryptPKCS1v15(rand.Reader, pubInterface.(*rsa.PublicKey), []byte(origData))
	return strings.ToUpper(hex.EncodeToString(data))
}

// aes 加密params
func AesECBEncrypt(data, key string) string {
	block, _ := aes.NewCipher([]byte(key))
	paddingData := PKCS7Padding([]byte(data), block.BlockSize())
	decrypted := make([]byte, len(paddingData))
	size := block.BlockSize()
	for src, dst := paddingData, decrypted; len(src) > 0; src, dst = src[size:], dst[size:] {
		block.Encrypt(dst[:size], src[:size])
	}
	return strings.ToUpper(hex.EncodeToString(decrypted))
}

func PKCS7Padding(ciphertext []byte, blockSize int) []byte {
	padding := blockSize - len(ciphertext)%blockSize
	padtext := bytes.Repeat([]byte{byte(padding)}, padding)
	return append(ciphertext, padtext...)
}

// 获取http规范的时间
func getHttpDateStr() string {
	return time.Now().UTC().Format(http.TimeFormat)
}

// 时间戳
func timestamp() int64 {
	return time.Now().UTC().UnixNano() / 1e6
}

// formatDate formats a time.Time object into the "YYYY-MM-DDHH:mm:ssSSS" format.
func formatDate(t time.Time) string {
	// The layout string "2006-01-0215:04:05.000" corresponds to:
	// 2006 -> Year (YYYY)
	// 01   -> Month (MM)
	// 02   -> Day (DD)
	// 15   -> Hour (HH)
	// 04   -> Minute (mm)
	// 05   -> Second (ss)
	// 000  -> Millisecond (SSS) with leading zeros
	// Note the lack of a separator between the date and hour, matching the desired output.
	return t.Format("2006-01-0215:04:05.000")
}

func MustParseTime(str string) *time.Time {
	lastOpTime, _ := time.ParseInLocation("2006-01-02 15:04:05 -07", str+" +08", time.Local)
	return &lastOpTime
}

type Time time.Time

func (t *Time) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return err
	}
	return t.Unmarshal([]byte(s))
}
func (t *Time) UnmarshalXML(e *xml.Decoder, ee xml.StartElement) error {
	b, err := e.Token()
	if err != nil {
		return err
	}
	if b, ok := b.(xml.CharData); ok {
		if err = t.Unmarshal(b); err != nil {
			return err
		}
	}
	return e.Skip()
}
func (t *Time) Unmarshal(b []byte) error {
	bs := strings.Trim(string(b), "\"")
	// 189 时间串里 AM/PM 前可能使用 U+202F 窄不换行空格或 U+00A0 不换行空格，统一替换为普通空格
	bs = strings.ReplaceAll(bs, "\u202f", " ")
	bs = strings.ReplaceAll(bs, "\u00a0", " ")
	var v time.Time
	var err error
	// 189 返回的时间可能自带时区（如 "Aug 11, 2026, 10:37:18 PM +08"），也可能不带，分别尝试
	for _, s := range []string{bs, bs + " +08"} {
		for _, f := range []string{"2006-01-02 15:04:05 -07", "Jan 2, 2006 3:04:05 PM -07", "Jan 2, 2006, 3:04:05 PM -07"} {
			v, err = time.ParseInLocation(f, s, utils.CNLoc)
			if err == nil {
				break
			}
		}
		if err == nil {
			break
		}
	}
	*t = Time(v)
	return err
}

type String string

func (t *String) UnmarshalJSON(b []byte) error { return t.Unmarshal(b) }
func (t *String) UnmarshalXML(e *xml.Decoder, ee xml.StartElement) error {
	b, err := e.Token()
	if err != nil {
		return err
	}
	if b, ok := b.(xml.CharData); ok {
		if err = t.Unmarshal(b); err != nil {
			return err
		}
	}
	return e.Skip()
}
func (s *String) Unmarshal(b []byte) error {
	*s = String(bytes.Trim(b, "\""))
	return nil
}

func toFamilyOrderBy(o string) string {
	switch o {
	case "filename":
		return "1"
	case "filesize":
		return "2"
	case "lastOpTime":
		return "3"
	default:
		return "1"
	}
}

func toDesc(o string) string {
	switch o {
	case "desc":
		return "true"
	case "asc":
		fallthrough
	default:
		return "false"
	}
}

func ParseHttpHeader(str string) map[string]string {
	header := make(map[string]string)
	for _, value := range strings.Split(str, "&") {
		if k, v, found := strings.Cut(value, "="); found {
			header[k] = v
		}
	}
	return header
}

func MustString(str string, err error) string {
	return str
}

func BoolToNumber(b bool) int {
	if b {
		return 1
	}
	return 0
}

// 计算分片大小
// 对分片数量有限制
// 10MIB 20 MIB 999片
// 50MIB 60MIB 70MIB 80MIB ∞MIB 1999片
//
// 规则由文件大小唯一决定，与用户配置无关；实现放在 pkg/torrent.CASSliceSize，
// 保证上传侧、种子生成侧、秒传侧共用同一份定义。
func partSize(size int64) int64 {
	return torrent.CASSliceSize(size)
}

// casSliceSizeByLimit 与 torrent.CASSliceSize 同规则，但片数上限可指定。
//
// 上游/本项目用的是 999；CAS 生成端（MyMediaHelper）实测后改用 1000，
// 于是边界窗口 `(999*10MiB, 1000*10MiB]` 与 `(2*999*10MiB, 2*1000*10MiB]`
// 两侧算出的分片大小会差一档：生成端写 10MiB 的 sliceMd5，消费端却按 20MiB
// 去比对，必然 fileDataExists=0。
func casSliceSizeByLimit(size int64, limit int64) int64 {
	const def = int64(10 * 1024 * 1024)
	if limit <= 0 {
		limit = 999
	}
	if size > def*2*limit {
		multiple := int64(math.Ceil((float64(size) / 1999) / float64(def)))
		if multiple < 5 {
			multiple = 5
		}
		return multiple * def
	}
	if size > def*limit {
		return def * 2
	}
	return def
}

// casSliceSizeCandidates 返回去重后的分片大小候选，标准档位在前。
//
// `.cas` 载荷不记录分片大小（只有五个字段），所以无法从文件自证它当初是按
// 哪一档算的；天翼又是按客户端上报的 sliceSize 现场重算判重。恢复时只能
// 逐档探测：标准档不命中就换一档再问，命中即 commit。
// 只枚举**比标准档更小**的档位（口径差异只会让另一侧算出更小的分片），
// 绝大多数文件因此只有一项，行为与单档完全一致。
func casSliceSizeCandidates(size int64) []int64 {
	const (
		def      = int64(10 * 1024 * 1024)
		maxParts = int64(1999)
	)
	standard := partSize(size)
	candidates := []int64{standard}
	for _, sliceSize := range []int64{casSliceSizeByLimit(size, 1000), def * 2, def} {
		if sliceSize >= standard || sliceSize < def {
			continue
		}
		// 片数超上限的档位必然被服务端拒绝，不必浪费请求
		if size > 0 && (size+sliceSize-1)/sliceSize > maxParts {
			continue
		}
		duplicated := false
		for _, existing := range candidates {
			if existing == sliceSize {
				duplicated = true
				break
			}
		}
		if !duplicated {
			candidates = append(candidates, sliceSize)
		}
	}
	return candidates
}

// formatSliceSize 仅用于错误信息，避免为一行日志引入额外依赖。
func formatSliceSize(sliceSize int64) string {
	const mib = int64(1024 * 1024)
	if sliceSize%mib == 0 {
		return fmt.Sprintf("%dMiB", sliceSize/mib)
	}
	return fmt.Sprintf("%dB", sliceSize)
}


func isBool(bs ...bool) bool {
	for _, b := range bs {
		if b {
			return true
		}
	}
	return false
}

func IF[V any](o bool, t V, f V) V {
	if o {
		return t
	}
	return f
}

type WrapFileStreamer struct {
	model.FileStreamer
	Name string
}

func (w *WrapFileStreamer) GetName() string {
	return w.Name
}
