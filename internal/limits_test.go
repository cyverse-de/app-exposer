package internal

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestLabelValueReplacement(t *testing.T) {
	assert := assert.New(t)

	assert.Equal("foo-xxx-u", labelValueString("foo_"))
	assert.Equal("foo-xxx-u-u", labelValueString("foo__"))
	assert.Equal("foo-xxx-u-h-u", labelValueString("foo_-_"))
	assert.Equal("h-xxx-foo", labelValueString("-foo"))
	assert.Equal("h-u-h-xxx-foo", labelValueString("-_-foo"))
	assert.Equal("h-u-h-xxx-foo-bar-xxx-h-u-h", labelValueString("-_-foo-bar-_-"))
	assert.Equal("u-u-u-xxx-foo_bar-xxx-u-u-u", labelValueString("___foo_bar___"))
	assert.Equal("u-u-u-u-xxx-foo__bar-baz__quux-xxx-u-u-u-u", labelValueString("____foo__bar--baz__quux____"))
}
