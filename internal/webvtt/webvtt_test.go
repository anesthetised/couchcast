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
 
Сегодня<00:00:01.599><c> готовим</c><00:00:01.959><c> простой</c><00:00:02.480><c> ужин,</c><00:00:02.960><c> для</c>

00:00:03.189 --> 00:00:03.199 align:start position:0%
Сегодня готовим простой ужин, для
 

00:00:03.199 --> 00:00:04.510 align:start position:0%
Сегодня готовим простой ужин, для
которого<00:00:03.679><c> понадобятся</c><00:00:03.959><c> всего</c><00:00:04.040><c> три</c>

00:00:04.510 --> 00:00:04.520 align:start position:0%
которого понадобятся всего три
 

00:00:04.520 --> 00:00:06.190 align:start position:0%
которого понадобятся всего три
продукта<00:00:04.960><c> и</c><00:00:05.520><c> двадцать</c><00:00:05.640><c> минут</c><00:00:05.799><c> времени.</c>

00:00:06.190 --> 00:00:06.200 align:start position:0%
продукта и двадцать минут времени.
 

00:00:09.000 --> 00:00:10.000 align:start position:0%
продукта и двадцать минут времени.
Приятного<00:00:09.480><c> аппетита.</c>
`

func TestNormalizeRollUp(t *testing.T) {
	want := `WEBVTT
Kind: captions
Language: ru

00:00:01.400 --> 00:00:04.510
Сегодня готовим простой ужин, для
которого понадобятся всего три

00:00:04.520 --> 00:00:06.190
продукта и двадцать минут времени.

00:00:09.000 --> 00:00:10.000
Приятного аппетита.

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
