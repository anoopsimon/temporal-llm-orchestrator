import nextra from 'nextra'

const withNextra = nextra({
  theme: 'nextra-theme-docs',
  themeConfig: './theme.config.jsx'
})

const rawBasePath = process.env.BASE_PATH || ''
const basePath = process.env.NODE_ENV === 'production' ? rawBasePath : ''

export default withNextra({
  output: 'export',
  env: {
    // Disable Nextra client-side search to reduce bundle size for this small docs site.
    NEXTRA_SEARCH: ''
  },
  trailingSlash: true,
  images: {
    unoptimized: true
  },
  basePath,
  assetPrefix: basePath ? `${basePath}/` : undefined
})
