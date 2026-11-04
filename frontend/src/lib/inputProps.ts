// rawTextProps keeps mobile keyboards from capitalizing or autocorrecting
// fields that hold machine-readable values (domains, tags, usernames).
export const rawTextProps = {
  autoCapitalize: 'none',
  autoCorrect: 'off',
  spellCheck: false,
} as const
