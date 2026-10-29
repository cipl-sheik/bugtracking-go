package bugtrack

import (
	"path/filepath"
	"runtime"
	"strings"
)

const sdkFramePrefix = "github.com/cipl/bugtracking-gopackage."

func captureFrames() []frame {
	var pcs [maxStackFrames + 32]uintptr
	n := runtime.Callers(2, pcs[:])
	callers := runtime.CallersFrames(pcs[:n])
	out := make([]frame, 0, n)
	for {
		fr, more := callers.Next()
		if fr.Function != "" && !isSDKFrame(fr.Function) {
			out = append(out, toFrame(fr))
			if len(out) >= maxStackFrames {
				break
			}
		}
		if !more {
			break
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func isSDKFrame(fn string) bool {
	// The module prefix covers normal SDK frames. The middleware closure can be
	// inlined into the caller, which changes that prefix, so match the closure too.
	return strings.Contains(fn, sdkFramePrefix) || strings.Contains(fn, ".HTTPMiddleware.func")
}

func toFrame(fr runtime.Frame) frame {
	path := filepath.ToSlash(fr.File)
	return frame{
		Filename:   path,
		Function:   fr.Function,
		LineNumber: fr.Line,
		AbsPath:    path,
		InApp:      inApp(fr.File, fr.Function),
		Module:     packagePath(fr.Function),
	}
}

func inApp(file, fn string) bool {
	if strings.HasPrefix(fn, "runtime.") {
		return false
	}
	root := runtime.GOROOT()
	if root != "" && file != "" && strings.HasPrefix(filepath.Clean(file), filepath.Clean(root)) {
		return false
	}
	return true
}

func packagePath(fn string) string {
	if i := strings.Index(fn, ".("); i > 0 {
		return fn[:i]
	}
	if i := strings.LastIndex(fn, "."); i > 0 {
		return fn[:i]
	}
	return ""
}

func applyTopFrame(ex *exceptionBody, frames []frame) {
	for _, fr := range frames {
		if !fr.InApp {
			continue
		}
		ex.File = fr.Filename
		ex.Function = fr.Function
		if fr.LineNumber > 0 {
			line := uint(fr.LineNumber)
			ex.Line = &line
		}
		return
	}
}
