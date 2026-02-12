import React from 'react'

const repoURL = process.env.NEXT_PUBLIC_REPO_URL || 'https://github.com'

const config = {
  logo: <span style={{ fontWeight: 700 }}>AI Intake Workflow Docs</span>,
  project: {
    link: repoURL
  },
  docsRepositoryBase: `${repoURL}/tree/main/docs`,
  footer: {
    text: `AI Document Intake and Decision Workflow`
  },
  useNextSeoProps() {
    return {
      titleTemplate: '%s - AI Intake Workflow'
    }
  },
  primaryHue: 204,
  primarySaturation: 93
}

export default config
