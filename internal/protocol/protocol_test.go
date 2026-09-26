package protocol

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDecode(t *testing.T) {
	typ, msg, err := Decode([]byte(`{"type":"seek","positionMs":1500}`))
	require.NoError(t, err)
	assert.Equal(t, TypeSeek, typ)
	assert.Equal(t, int64(1500), msg.(*Seek).PositionMs)

	typ, msg, err = Decode([]byte(`{"type":"pause"}`))
	require.NoError(t, err)
	assert.Equal(t, TypePause, typ)
	assert.Nil(t, msg)

	_, msg, err = Decode([]byte(`{"type":"play"}`))
	require.NoError(t, err)
	assert.False(t, msg.(*Play).Countdown)
	_, msg, err = Decode([]byte(`{"type":"play","countdown":true}`))
	require.NoError(t, err)
	assert.True(t, msg.(*Play).Countdown)

	_, _, err = Decode([]byte(`{"type":"nope"}`))
	assert.Error(t, err)
	_, _, err = Decode([]byte(`{"type":"seek","positionMs":"x"}`))
	assert.Error(t, err)
	_, _, err = Decode([]byte(`not json`))
	assert.Error(t, err)

	typ, msg, err = Decode([]byte(`{"type":"queue.move","itemId":"3e5a8b1a-6b6f-4c2c-9c0e-3d3a1c1b2f00","afterId":null}`))
	require.NoError(t, err)
	assert.Equal(t, TypeQueueMove, typ)
	assert.Nil(t, msg.(*QueueMove).AfterID)
}

func TestDecodeEnvelopeRef(t *testing.T) {
	env, msg, err := DecodeEnvelope([]byte(`{"type":"seek","positionMs":1500,"ref":9}`))
	require.NoError(t, err)
	assert.Equal(t, Envelope{Type: TypeSeek, Ref: 9}, env)
	assert.Equal(t, &Seek{PositionMs: 1500}, msg)

	env, _, err = DecodeEnvelope([]byte(`{"type":"nope","ref":3}`))
	require.Error(t, err)
	assert.EqualValues(t, 3, env.Ref, "an unknown command still names itself")

	env, _, err = DecodeEnvelope([]byte(`{"type":"pause"}`))
	require.NoError(t, err)
	assert.Zero(t, env.Ref, "ref is optional")
}
