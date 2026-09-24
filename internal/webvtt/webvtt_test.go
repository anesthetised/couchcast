package webvtt

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// A trimmed YouTube automatic caption file, as yt-dlp writes it.
const rollUp = `WEBVTT
Kind: captions
Language: ru

00:00:01.400 --> 00:00:03.189 align:start position:0%
 
Меня<00:00:01.599><c> признали</c><00:00:01.959><c> иностранным</c><00:00:02.480><c> агентом,</c><00:00:02.960><c> хотя</c>

00:00:03.189 --> 00:00:03.199 align:start position:0%
Меня признали иностранным агентом, хотя
 

00:00:03.199 --> 00:00:04.510 align:start position:0%
Меня признали иностранным агентом, хотя
Минюст<00:00:03.679><c> сказал,</c><00:00:03.959><c> что</c><00:00:04.040><c> иностранных</c>

00:00:04.510 --> 00:00:04.520 align:start position:0%
Минюст сказал, что иностранных
 

00:00:04.520 --> 00:00:06.190 align:start position:0%
Минюст сказал, что иностранных
источников<00:00:04.960><c> финансирования</c><00:00:05.520><c> у</c><00:00:05.640><c> меня</c><00:00:05.799><c> нет.</c>

00:00:06.190 --> 00:00:06.200 align:start position:0%
источников финансирования у меня нет.
 

00:00:09.000 --> 00:00:10.000 align:start position:0%
источников финансирования у меня нет.
Приятного<00:00:09.480><c> просмотра.</c>
`

func TestNormalizeRollUp(t *testing.T) {
	want := `WEBVTT
Kind: captions
Language: ru

00:00:01.400 --> 00:00:04.510
Меня признали иностранным агентом, хотя
Минюст сказал, что иностранных

00:00:04.520 --> 00:00:06.190
источников финансирования у меня нет.

00:00:09.000 --> 00:00:10.000
Приятного просмотра.

`
	assert.Equal(t, want, string(Normalize([]byte(rollUp))))
}

func TestNormalizePlain(t *testing.T) {
	in := "WEBVTT\r\n\r\nNOTE kept\r\n\r\nintro\r\n01:47.250 --> 01:50.500 align:start position:0%\r\nThis blade has a dark past.\r\n \r\n\r\n00:01:51.800 --> 00:01:55.800 line:10%\r\nIt has shed\r\nmuch innocent blood.\r\n\r\n00:02:00.000 --> 00:02:01.000\r\n \r\n"
	want := "WEBVTT\n\nNOTE kept\n\nintro\n00:01:47.250 --> 00:01:50.500\nThis blade has a dark past.\n\n00:01:51.800 --> 00:01:55.800 line:10%\nIt has shed\nmuch innocent blood.\n\n"
	assert.Equal(t, want, string(Normalize([]byte(in))))
}

func TestNormalizeNotVTT(t *testing.T) {
	in := []byte("1\n00:00:01,000 --> 00:00:02,000\nsrt\n")
	assert.Equal(t, in, Normalize(in))
}
