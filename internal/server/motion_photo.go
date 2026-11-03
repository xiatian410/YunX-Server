package server

// Motion Photo container follows YunX-Desktop's MotionPhotoWriter.kt (AGPL-3.0).
// Keep both the still and motion media; never silently discard the motion track.
import (
	"context"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"image/jpeg"
	"io"
	"os"
	"os/exec"
	"path/filepath"
)

func makeMotionPhoto(ctx context.Context, work string) (string, error) {
	still := filepath.Join(work, "part-0")
	motion := filepath.Join(work, "motion.mp4")
	input, e := os.Open(still)
	if e != nil {
		return "", e
	}
	head := make([]byte, 2)
	_, e = io.ReadFull(input, head)
	input.Close()
	if e != nil {
		return "", e
	}
	if head[0] != 0xff || head[1] != 0xd8 {
		still = filepath.Join(work, "still.jpg")
		if e = exec.CommandContext(ctx, "ffmpeg", "-nostdin", "-v", "error", "-y", "-i", filepath.Join(work, "part-0"), "-frames:v", "1", still).Run(); e != nil {
			return "", fail("motion_still", "动态照片静图转换失败，已保留原始文件")
		}
	}
	if e = exec.CommandContext(ctx, "ffmpeg", "-nostdin", "-v", "error", "-y", "-i", filepath.Join(work, "part-1"), "-map", "0:v:0", "-map", "0:a?", "-c", "copy", "-movflags", "+faststart", motion).Run(); e != nil {
		return "", fail("motion_video", "动态照片视频整理失败，已保留原始文件")
	}
	out := filepath.Join(work, "motion-photo.jpg")
	if e = writeMotionPhoto(still, motion, out); e != nil {
		return "", e
	}
	return out, nil
}
func writeMotionPhoto(still, motion, output string) error {
	photo, e := os.Open(still)
	if e != nil {
		return e
	}
	defer photo.Close()
	dimensions, e := jpeg.DecodeConfig(photo)
	if e != nil {
		return e
	}
	if _, e = photo.Seek(2, 0); e != nil {
		return e
	}
	video, e := os.Open(motion)
	if e != nil {
		return e
	}
	defer video.Close()
	stat, e := video.Stat()
	if e != nil {
		return e
	}
	if stat.Size() <= 0 {
		return fail("motion_empty", "动态照片的视频为空")
	}
	// XMP's video length is measured from EOF, independently of JPEG APP segments.
	xmp := fmt.Sprintf(`http://ns.adobe.com/xap/1.0/`+"\x00"+`<x:xmpmeta xmlns:x="adobe:ns:meta/"><rdf:RDF xmlns:rdf="http://www.w3.org/1999/02/22-rdf-syntax-ns#"><rdf:Description xmlns:GCamera="http://ns.google.com/photos/1.0/camera/" xmlns:Container="http://ns.google.com/photos/1.0/container/" xmlns:Item="http://ns.google.com/photos/1.0/container/item/" GCamera:MotionPhoto="1" GCamera:MotionPhotoVersion="1" GCamera:MotionPhotoPresentationTimestampUs="-1"><Container:Directory><rdf:Seq><rdf:li rdf:parseType="Resource"><Container:Item Item:Mime="image/jpeg" Item:Semantic="Primary" Item:Length="0" Item:Padding="0"/></rdf:li><rdf:li rdf:parseType="Resource"><Container:Item Item:Mime="video/mp4" Item:Semantic="MotionPhoto" Item:Length="%d" Item:Padding="0"/></rdf:li></rdf:Seq></Container:Directory></rdf:Description></rdf:RDF></x:xmpmeta>`, stat.Size())
	exif, _ := hex.DecodeString("FFE100724578696600004D4D002A0000000800040100000400000001000005A001010004000000010000043C87690004000000010000003E011200030000000100000000000000000002928600020000000E0000005C920800040000000100000000000000006F706C75735F3833383836303800")
	binary.BigEndian.PutUint32(exif[28:32], uint32(dimensions.Width))
	binary.BigEndian.PutUint32(exif[40:44], uint32(dimensions.Height))
	target, e := os.OpenFile(output, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
	if e != nil {
		return e
	}
	defer target.Close()
	header := []byte{0xff, 0xd8, 0xff, 0xe1, 0, 0}
	binary.BigEndian.PutUint16(header[4:6], uint16(len(xmp)+2))
	for _, data := range [][]byte{header, []byte(xmp), exif} {
		if _, e = target.Write(data); e != nil {
			return e
		}
	}
	if _, e = io.Copy(target, photo); e != nil {
		return e
	}
	if _, e = io.Copy(target, video); e != nil {
		return e
	}
	return target.Close()
}
