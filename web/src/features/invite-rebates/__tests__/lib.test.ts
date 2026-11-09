import { describe, expect, it } from 'vitest'

import { buildInviteLink } from '../lib'

describe('buildInviteLink', () => {
  it('uses the xingya.space sign-up route', () => {
    expect(buildInviteLink('XY7K9Q')).toBe(
      'https://xingya.space/sign-up?aff=XY7K9Q'
    )
  })

  it('trims the code and encodes it safely', () => {
    expect(buildInviteLink(' a b ')).toBe(
      'https://xingya.space/sign-up?aff=a%20b'
    )
  })

  it('returns an empty string when the code is missing', () => {
    expect(buildInviteLink('   ')).toBe('')
  })
})
