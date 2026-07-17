package record

import "strings"

// StringSlice is a repeatable flag.Value: each -task occurrence appends one
// value, so `record -task A -task B` yields []string{"A", "B"}.
type StringSlice []string

// String renders the accumulated values for flag help.
func (s *StringSlice) String() string { return strings.Join(*s, ", ") }

// Set appends one flag occurrence.
func (s *StringSlice) Set(v string) error {
	*s = append(*s, v)
	return nil
}

// ResolveVideo applies the -video/-no-video precedence. Audio-only is the
// default (video false); -video opts in; -no-video is the explicit symmetric
// off and wins when both are given.
func ResolveVideo(video, noVideo bool) bool {
	return video && !noVideo
}
