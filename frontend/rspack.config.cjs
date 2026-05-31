const path = require("node:path");
const { HtmlRspackPlugin } = require("@rspack/core");

module.exports = {
  context: __dirname,
  entry: "./src/main.tsx",
  output: {
    path: path.resolve(__dirname, "dist"),
    filename: "assets/[name].[contenthash:8].js",
    clean: true,
    publicPath: "/",
  },
  module: {
    rules: [
      {
        test: /\.[jt]sx?$/,
        exclude: /node_modules/,
        loader: "builtin:swc-loader",
        options: {
          jsc: {
            parser: {
              syntax: "typescript",
              tsx: true,
            },
            transform: {
              react: {
                runtime: "automatic",
              },
            },
          },
        },
        type: "javascript/auto",
      },
      {
        test: /\.module\.css$/,
        type: "css/module",
        parser: {
          namedExports: false,
        },
        generator: {
          esModule: true,
          exportsConvention: "camel-case-only",
        },
      },
      {
        test: /\.module\.s[ac]ss$/,
        use: [
          {
            loader: "sass-loader",
            options: {
              api: "modern",
              implementation: require("sass-embedded"),
            },
          },
        ],
        type: "css/module",
        parser: {
          namedExports: false,
        },
        generator: {
          esModule: true,
          exportsConvention: "camel-case-only",
        },
      },
      {
        test: /\.css$/,
        exclude: /\.module\.css$/,
        type: "css",
      },
      {
        test: /\.s[ac]ss$/,
        exclude: /\.module\.s[ac]ss$/,
        use: [
          {
            loader: "sass-loader",
            options: {
              api: "modern",
              implementation: require("sass-embedded"),
            },
          },
        ],
        type: "css",
      },
    ],
  },
  experiments: {
    css: true,
  },
  resolve: {
    extensions: [".ts", ".tsx", ".js", ".jsx"],
  },
  plugins: [
    new HtmlRspackPlugin({
      title: "NiceAgent",
      template: "./src/index.html",
    }),
  ],
  devServer: {
    historyApiFallback: true,
    port: 3000,
    proxy: [
      {
        context: ["/api", "/healthz"],
        target: "http://127.0.0.1:8080",
      },
    ],
  },
};
