// @ts-check
import { defineConfig } from 'astro/config';
import starlight from '@astrojs/starlight';

// https://astro.build/config
export default defineConfig({
	integrations: [
		starlight({
			title: 'AxiaOps',
			logo: {
				src: './src/assets/axiaops-logo-dark.svg',
				replacesTitle: true,
			},
			favicon: '/favicon.svg',
			customCss: ['./src/styles/custom.css'],
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
					],
				},
			],
		}),
	],
});
