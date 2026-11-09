export const INVITE_SIGNUP_ORIGIN = 'https://xingya.space'

export function buildInviteLink(code: string): string {
  const trimmed = code.trim()
  if (!trimmed) return ''
  return `${INVITE_SIGNUP_ORIGIN}/sign-up?aff=${encodeURIComponent(trimmed)}`
}
