// @ts-check
import { defineConfig } from 'astro/config';
import starlight from '@astrojs/starlight';

// https://astro.build/config
export default defineConfig({
	site: 'https://axiaops.io',
	base: '/',
	integrations: [
		starlight({
			title: 'AxiaOps',
			logo: {
				dark: './src/assets/axiaops-logo-dark.svg',
				light: './src/assets/axiaops-logo.svg',
				replacesTitle: true,
			},
			favicon: '/favicon.svg',
			customCss: ['./src/styles/custom.css'],
			social: [
				{ icon: 'github', label: 'GitHub', href: 'https://github.com/ahmd-soliman/axiaops' },
			],
			components: {
				// Dark/Light only -- see the override's own comment for why
				// "Auto" was dropped.
				ThemeSelect: './src/components/ThemeSelect.astro',
			},
			sidebar: [
				{
					label: 'Overview',
					items: [{ label: 'What is AxiaOps', slug: 'index' }],
				},
				{
					label: 'Guides',
					items: [
						{ label: 'Architecture', slug: 'guides/architecture' },
						{ label: 'Deployment', slug: 'guides/deployment' },
						{ label: 'Deploying on AWS', slug: 'guides/aws-deployment' },
						{ label: 'Deploying on ECS', slug: 'guides/ecs-deployment' },
						{ label: 'Authentication & Roles', slug: 'guides/authentication' },
						{ label: 'Operations', slug: 'guides/operations' },
						{ label: 'Observability', slug: 'guides/observability' },
					],
				},
				{
					label: 'Contributing',
					items: [
						{ label: 'Getting Started', slug: 'guides/contributing' },
						{ label: 'Testing', slug: 'guides/testing' },
					],
				},
			],
		}),
	],
});
