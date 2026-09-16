/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
import { Link } from '@tanstack/react-router'
import {
  BookOpen,
  Info,
  KeyRound,
  LifeBuoy,
  MessageCircle,
  Plug,
  ShoppingCart,
  Signpost,
  Sparkles,
  TerminalSquare,
  Wallet,
  Zap,
} from 'lucide-react'
import type { ReactNode } from 'react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'

import { API_BASE, QQ_GROUP, QQ_JOIN_URL, SHOP_URL } from '../lib/constants'

const cardClass =
  'rounded-2xl border border-border/60 bg-card p-4 shadow-sm sm:p-6'

const featureCardClass =
  'rounded-xl border border-border/60 bg-muted/30 p-4'

// Code samples are intentionally not translated: they are copy-paste artefacts,
// so the placeholder key and message stay ASCII.
const CURL_SNIPPET = `curl ${API_BASE}/chat/completions \\
  -H "Authorization: Bearer sk-YOUR_API_KEY" \\
  -H "Content-Type: application/json" \\
  -d '{"model":"deepseek-chat","messages":[{"role":"user","content":"Hello"}]}'`

const PYTHON_SNIPPET = `from openai import OpenAI

client = OpenAI(base_url="${API_BASE}", api_key="sk-YOUR_API_KEY")
resp = client.chat.completions.create(
    model="deepseek-chat",
    messages=[{"role": "user", "content": "Hello"}],
)
print(resp.choices[0].message.content)`

const JS_SNIPPET = `import OpenAI from "openai";

const client = new OpenAI({
  baseURL: "${API_BASE}",
  apiKey: "sk-YOUR_API_KEY",
});
const resp = await client.chat.completions.create({
  model: "deepseek-chat",
  messages: [{ role: "user", content: "Hello" }],
});
console.log(resp.choices[0].message.content);`

export function BrandSection() {
  const { t } = useTranslation()

  const features = [
    {
      icon: Sparkles,
      title: t('Multiple models in one place'),
      desc: t(
        'One account reaches DeepSeek, GLM, Qwen, Doubao, Claude and other mainstream models, with no separate sign-ups or billing.'
      ),
    },
    {
      icon: Plug,
      title: t('OpenAI compatible'),
      desc: t(
        'Fully compatible with the OpenAI API format; existing code only needs its base_url and key changed.'
      ),
    },
    {
      icon: Wallet,
      title: t('Transparent billing'),
      desc: t(
        'Billing is in 「芽点」, priced by token or per call, and usage is always visible in the dashboard.'
      ),
    },
    {
      icon: Zap,
      title: t('Stable and low latency'),
      desc: t(
        'Cloudflare plus a high-performance gateway, with native streaming (SSE) and long-connection support.'
      ),
    },
    {
      icon: LifeBuoy,
      title: t('Official support'),
      desc: t(
        'Join the Xingya AI Group 2 for help; admins and helpful users are around to answer.'
      ),
    },
    {
      icon: Info,
      title: t('Site information'),
      desc: t('Main site {{site}}, endpoint {{api}}.', {
        site: 'https://xingya.site',
        api: API_BASE,
      }),
    },
  ]

  return (
    <section className='mx-auto w-full max-w-3xl px-4'>
      <div className={cardClass}>
        <h2 className='mb-3 text-lg font-bold'>{t('What is Xingya?')}</h2>
        <p className='text-foreground/85 text-sm leading-7'>
          {t(
            'Xingya is a lightweight, stable AI API aggregation and relay platform. It does not run models itself; it brings DeepSeek, GLM, Qwen, Claude and other upstream models under one endpoint: register one account, create one API Key, and call every model in an OpenAI-compatible way, billed by usage.'
          )}
        </p>
        <div className='mt-4 grid gap-3 sm:grid-cols-2'>
          {features.map((feature) => (
            <div key={feature.title} className={featureCardClass}>
              <div className='mb-2 flex items-center gap-2 font-semibold'>
                <feature.icon
                  className='text-primary size-4'
                  aria-hidden='true'
                />
                {feature.title}
              </div>
              <p className='text-foreground/85 text-[13px] leading-6 sm:text-sm'>
                {feature.desc}
              </p>
            </div>
          ))}
        </div>
      </div>
    </section>
  )
}

function CodeBlock(props: { title: string; code: string }) {
  return (
    <div>
      <div className='text-muted-foreground mb-1.5 text-xs font-semibold'>
        {props.title}
      </div>
      <pre className='border-border/60 bg-muted/30 overflow-x-auto rounded-xl border p-3 text-xs leading-5'>
        <code>{props.code}</code>
      </pre>
    </div>
  )
}

export function TutorialSection() {
  const { t } = useTranslation()

  const steps: { icon: typeof BookOpen; title: string; body: ReactNode }[] = [
    {
      icon: Signpost,
      title: t('Register an account'),
      body: (
        <p>
          {t(
            'Click "Free sign-up" below or visit the main site {{site}} to register with a username, email and password.',
            { site: 'https://xingya.site' }
          )}
        </p>
      ),
    },
    {
      icon: KeyRound,
      title: t('Create an API Key'),
      body: (
        <p>
          {t(
            'After signing in, open "Tokens" and create one, choosing a suitable group. The key is shown only once — copy and store it right away; if it leaks, delete it and create a new one.'
          )}
        </p>
      ),
    },
    {
      icon: BookOpen,
      title: t('Choose a model'),
      body: (
        <p>
          {t(
            'See available models and rate notes on the "Models" page. Standard model names work as-is; names with a times/ prefix are billed per call.'
          )}
        </p>
      ),
    },
    {
      icon: TerminalSquare,
      title: t('Configure and call'),
      body: (
        <p>
          {t(
            'The endpoint is {{api}}; put your API Key in the key field and call it in an OpenAI-compatible way.',
            { api: API_BASE }
          )}
        </p>
      ),
    },
    {
      icon: Wallet,
      title: t('Recharge 芽点'),
      body: (
        <p>
          {t(
            'Buy a code on the official card shop and redeem it in "Wallet", or contact an admin for a manual top-up.'
          )}
        </p>
      ),
    },
    {
      icon: MessageCircle,
      title: t('Join the Xingya AI Group 2'),
      body: <p>{t('QQ group {{group}} — come in and ask whenever a problem comes up.', { group: QQ_GROUP })}</p>,
    },
  ]

  return (
    <section className='mx-auto w-full max-w-3xl px-4'>
      <div className={cardClass}>
        <h2 className='mb-4 text-lg font-bold'>
          {t('Platform basics tutorial')}
        </h2>
        <ol className='space-y-4'>
          {steps.map((step, index) => (
            <li
              key={step.title}
              className='rounded-xl border border-border/60 bg-muted/30 p-4'
            >
              <div className='flex items-center gap-2 font-semibold'>
                <span className='bg-primary flex size-7 shrink-0 items-center justify-center rounded-full text-xs font-bold text-white'>
                  {index + 1}
                </span>
                <step.icon className='text-primary size-4' aria-hidden='true' />
                {step.title}
              </div>
              <div className='text-foreground/85 mt-2 space-y-3 text-[13px] leading-6 sm:text-sm'>
                {step.body}
                {index === 3 && (
                  <div className='space-y-3'>
                    <CodeBlock title='cURL' code={CURL_SNIPPET} />
                    <CodeBlock
                      title={t('Python (openai SDK)')}
                      code={PYTHON_SNIPPET}
                    />
                    <CodeBlock
                      title={t('JavaScript (openai SDK)')}
                      code={JS_SNIPPET}
                    />
                  </div>
                )}
              </div>
            </li>
          ))}
        </ol>
        <div className='mt-5 flex flex-col gap-2.5 sm:flex-row'>
          <Button size='lg' render={<Link to='/sign-up' />}>
            {t('Free sign-up')}
          </Button>
          <Button
            size='lg'
            variant='outline'
            render={
              <a
                href={SHOP_URL}
                target='_blank'
                rel='noopener noreferrer'
              />
            }
          >
            <ShoppingCart className='size-4' aria-hidden='true' />
            {t('Go to the card shop')}
          </Button>
          <Button
            size='lg'
            variant='outline'
            render={
              <a
                href={QQ_JOIN_URL}
                target='_blank'
                rel='noopener noreferrer'
              />
            }
          >
            <MessageCircle className='size-4' aria-hidden='true' />
            {t('Click to join the Xingya AI Group 2 chat')}
          </Button>
        </div>
      </div>
    </section>
  )
}

export function FaqSection() {
  const { t } = useTranslation()

  const faqs = [
    {
      q: t('Where do I see my 芽点 balance?'),
      a: t(
        'Sign in and open the dashboard — the remaining 芽点 is shown there; the "Wallet" page also shows it along with top-up details.'
      ),
    },
    {
      q: t('Why does my API call fail?'),
      a: t(
        'The usual causes are a mismatched model or group, insufficient balance, or an invalid API Key. Check the endpoint, model name and key first, then look at the specific error in "Logs".'
      ),
    },
    {
      q: t('My API Key leaked — what now?'),
      a: t(
        'Delete that token on the "Tokens" page immediately and create a new one. The old key stops working at once and other keys are unaffected.'
      ),
    },
    {
      q: t('Are the models official?'),
      a: t(
        'Xingya aggregates upstream models through stable channels and their behaviour matches the official providers; the models actually available and their pricing are governed by the "Models" page in the dashboard.'
      ),
    },
  ]

  return (
    <section className='mx-auto w-full max-w-3xl px-4'>
      <div className={cardClass}>
        <h2 className='mb-4 text-lg font-bold'>{t('FAQ')}</h2>
        <div className='space-y-3'>
          {faqs.map((faq) => (
            <details
              key={faq.q}
              className='group rounded-xl border border-border/60 bg-muted/30 p-4'
            >
              <summary className='flex cursor-pointer list-none items-center gap-2 text-sm font-semibold'>
                <span className='flex-1'>{faq.q}</span>
                <span
                  className='text-primary transition-transform group-open:rotate-45'
                  aria-hidden='true'
                >
                  +
                </span>
              </summary>
              <p className='text-foreground/85 mt-2 text-[13px] leading-6 sm:text-sm'>
                {faq.a}
              </p>
            </details>
          ))}
        </div>
      </div>
    </section>
  )
}
