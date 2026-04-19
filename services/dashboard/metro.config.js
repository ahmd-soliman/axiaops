// metro.config.js
//
// Forces Metro to resolve "react" and "react-dom" to the single copy installed
// at the project root, eliminating the duplicate-React error that arises when
// expo-router bundles its own nested React 18 alongside the app's React 19.
//
// The `overrides` field in package.json achieves the same after `npm install`;
// this alias covers local dev without requiring a reinstall.
const { getDefaultConfig } = require('expo/metro-config');
const path = require('path');

const config = getDefaultConfig(__dirname);

// Point every "react" import — from any depth of node_modules — to the single
// root-level copy so React's internal identity checks never see two instances.
config.resolver.extraNodeModules = {
  ...config.resolver.extraNodeModules,
  react: path.resolve(__dirname, 'node_modules/react'),
  'react-dom': path.resolve(__dirname, 'node_modules/react-dom'),
  'react/jsx-runtime': path.resolve(__dirname, 'node_modules/react/jsx-runtime'),
  'react/jsx-dev-runtime': path.resolve(__dirname, 'node_modules/react/jsx-dev-runtime'),
};

module.exports = config;
