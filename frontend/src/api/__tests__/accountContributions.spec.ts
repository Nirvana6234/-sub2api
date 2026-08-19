import { beforeEach, describe, expect, it, vi } from 'vitest'

const post = vi.fn()
const get = vi.fn()

vi.mock('@/api/client', () => ({
  apiClient: { post, get }
}))

describe('account contribution OpenAI authorization api', () => {
  beforeEach(() => {
    post.mockReset()
	get.mockReset()
    post.mockResolvedValue({ data: {} })
	get.mockResolvedValue({ data: [] })
  })

  it('uses the user contribution endpoint for every OpenAI login method', async () => {
    const api = await import('@/api/accountContributions')

    await api.generateOpenAIContributionAuthURL({ proxy_id: 7 })
    await api.createOpenAIContributionFromCode({ session_id: 'session', code: 'code', state: 'state' })
    await api.createOpenAIContributionFromRefreshToken({ refresh_token: 'rt-standard' })
    await api.createOpenAIContributionFromRefreshToken({ refresh_token: 'rt-mobile' }, true)
    await api.createOpenAIContributionFromCodexPAT({ access_token: 'at-test' })

    expect(post).toHaveBeenNthCalledWith(1, '/account-contributions/openai/generate-auth-url', { proxy_id: 7 })
    expect(post).toHaveBeenNthCalledWith(2, '/account-contributions/openai/create-from-code', { session_id: 'session', code: 'code', state: 'state' })
    expect(post).toHaveBeenNthCalledWith(3, '/account-contributions/openai/create-from-refresh-token', { refresh_token: 'rt-standard' })
    expect(post).toHaveBeenNthCalledWith(4, '/account-contributions/openai/create-from-mobile-refresh-token', { refresh_token: 'rt-mobile' })
    expect(post).toHaveBeenNthCalledWith(5, '/account-contributions/openai/create-from-codex-pat', { access_token: 'at-test' })
  })

	it('loads administrator-enabled contribution pool groups from the scoped endpoint', async () => {
		const api = await import('@/api/accountContributions')
		await api.listContributionPoolGroups()
		expect(get).toHaveBeenCalledWith('/account-contributions/pool-groups')
	})
})
