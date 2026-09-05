import type { RouteLocationNormalizedLoaded } from 'vue-router'
import { i18n } from '@/i18n'

type SeoInput = {
  route: RouteLocationNormalizedLoaded
  siteName?: string
}

function upsertMeta(key: string, attribute: 'name' | 'property', content: string) {
  let element = document.head.querySelector<HTMLMetaElement>(`meta[${attribute}="${key}"]`)
  if (!element) {
    element = document.createElement('meta')
    element.setAttribute(attribute, key)
    document.head.appendChild(element)
  }
  element.content = content
}

function removeMeta(key: string, attribute: 'name' | 'property') {
  document.head.querySelector(`meta[${attribute}="${key}"]`)?.remove()
}

function upsertCanonical(href: string) {
  let element = document.head.querySelector<HTMLLinkElement>('link[rel="canonical"]')
  if (!element) {
    element = document.createElement('link')
    element.rel = 'canonical'
    document.head.appendChild(element)
  }
  element.href = href
}

function translate(key: unknown, fallback: string) {
  if (typeof key !== 'string' || !key.trim()) return fallback
  const translated = i18n.global.t(key)
  return translated === key ? fallback : translated
}

function absoluteUrl(path: string) {
  return new URL(path, window.location.origin).href
}

function updateJsonLd(title: string, description: string, canonical: string, siteName: string, image?: string) {
  const existing = document.head.querySelector<HTMLScriptElement>('#sub2api-seo-jsonld')
  const script = existing ?? document.createElement('script')
  script.id = 'sub2api-seo-jsonld'
  script.type = 'application/ld+json'

  const graph: Record<string, unknown>[] = [
    {
      '@type': 'Organization',
      name: siteName,
      url: window.location.origin,
      ...(image ? { logo: image } : {}),
    },
    {
      '@type': 'WebSite',
      name: siteName,
      url: window.location.origin,
    },
    {
      '@type': 'SoftwareApplication',
      name: siteName,
      applicationCategory: 'DeveloperApplication',
      operatingSystem: 'Windows, macOS, Linux',
      url: window.location.origin,
    },
    {
      '@type': 'WebPage',
      name: title,
      description,
      url: canonical,
      isPartOf: { '@type': 'WebSite', url: window.location.origin },
    },
  ]

  script.textContent = JSON.stringify({ '@context': 'https://schema.org', '@graph': graph })
  if (!existing) document.head.appendChild(script)
}

export function useSeo() {
  function updateSeo({ route, siteName }: SeoInput) {
    const normalizedSiteName = siteName?.trim() || 'Sub2API'
    const fallbackTitle = `${String(route.meta.title || normalizedSiteName)} - ${normalizedSiteName}`
    const title = translate(route.meta.seoTitleKey, fallbackTitle)
    const description = translate(
      route.meta.seoDescriptionKey,
      'Sub2API AI API gateway and model service.',
    )
    const noindex = route.meta.noindex ?? !route.meta.seoTitleKey
    const canonical = absoluteUrl(window.location.pathname)
    const image = route.meta.seoImage ? absoluteUrl(route.meta.seoImage) : ''

    // Only pages that opt in with seoTitleKey get their <title> driven by this
    // module. Every other route's document.title is already correctly resolved
    // by resolveRouteDocumentTitle() (i18n titleKey + CustomPage menu overrides)
    // right before this runs — clobbering it here would silently break that.
    if (route.meta.seoTitleKey) {
      document.title = title
    }
    upsertMeta('description', 'name', description)
    upsertMeta('robots', 'name', noindex ? 'noindex,nofollow' : 'index,follow')
    upsertCanonical(canonical)
    upsertMeta('og:type', 'property', 'website')
    upsertMeta('og:title', 'property', title)
    upsertMeta('og:description', 'property', description)
    upsertMeta('og:url', 'property', canonical)
    upsertMeta('twitter:card', 'name', 'summary')
    upsertMeta('twitter:title', 'name', title)
    upsertMeta('twitter:description', 'name', description)

    if (image) {
      upsertMeta('og:image', 'property', image)
      upsertMeta('twitter:image', 'name', image)
    } else {
      removeMeta('og:image', 'property')
      removeMeta('twitter:image', 'name')
    }

    if (noindex) {
      document.head.querySelector<HTMLScriptElement>('#sub2api-seo-jsonld')?.remove()
    } else {
      updateJsonLd(title, description, canonical, normalizedSiteName, image || undefined)
    }
  }

  return { updateSeo }
}
