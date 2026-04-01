# frozen_string_literal: true

require "thor"
require "json"

module Git
  module Treeline
    class CLI < Thor
      desc "setup [PATH]", "Allocate resources and set up a worktree environment"
      option :main_repo, type: :string, desc: "Path to the main repository (auto-detected if omitted)"
      def setup(path = Dir.pwd)
        s = Setup.new(worktree_path: path, main_repo: options[:main_repo])
        s.run
      rescue Error => e
        warn "Error: #{e.message}"
        exit 1
      end

      desc "release [PATH]", "Release allocated resources for a worktree"
      option :drop_db, type: :boolean, default: false, desc: "Also drop the PostgreSQL database"
      def release(path = Dir.pwd)
        path = File.expand_path(path)
        registry = Git::Treeline.registry
        allocation = registry.find(path)

        unless allocation
          warn "No allocation found for #{path}"
          exit 1
        end

        if options[:drop_db] && allocation["database"]
          puts "==> Dropping database #{allocation["database"]}"
          system("dropdb", "--if-exists", allocation["database"])
        end

        registry.release(path)
        puts "==> Released resources for #{File.basename(path)}"
        ports = allocation["ports"] || [allocation["port"]]
        if ports.length > 1
          puts "  Ports:    #{ports.join(", ")}"
        else
          puts "  Port:     #{ports.first}"
        end
        puts "  Database: #{allocation["database"]}" if allocation["database"]
      end

      desc "status", "Show all active allocations across projects"
      option :project, type: :string, desc: "Filter by project name"
      option :json, type: :boolean, default: false, desc: "Output as JSON"
      def status
        registry = Git::Treeline.registry
        allocs = if options[:project]
                   registry.find_by_project(options[:project])
                 else
                   registry.allocations
                 end

        if options[:json]
          puts JSON.pretty_generate(allocs)
          return
        end

        if allocs.empty?
          puts "No active allocations."
          return
        end

        allocs.group_by { |a| a["project"] }.each do |project, entries|
          puts "\n#{project}:"
          entries.sort_by { |a| a["port"] || 0 }.each do |a|
            redis = a["redis_prefix"] ? "prefix:#{a["redis_prefix"]}" : "db:#{a["redis_db"]}"
            ports = a["ports"] || [a["port"]]
            port_label = ports.length > 1 ? ports.join(",") : ports.first.to_s
            puts "  :#{port_label}  #{a["worktree_name"]}  db:#{a["database"]}  #{redis}"
          end
        end
      end

      desc "prune", "Remove allocations for worktrees that no longer exist on disk"
      def prune
        count = Git::Treeline.registry.prune
        if count.zero?
          puts "Nothing to prune."
        else
          puts "Pruned #{count} stale allocation(s)."
        end
      end

      desc "init", "Generate a .treeline.yml config file for the current project"
      option :project, type: :string, desc: "Project name"
      option :template_db, type: :string, desc: "Template database name for cloning"
      def init
        path = File.join(Dir.pwd, PROJECT_CONFIG_FILE)
        if File.exist?(path)
          warn ".treeline.yml already exists"
          exit 1
        end

        # Ensure user-level config exists
        unless Git::Treeline.user_config.config_file_exists?
          Git::Treeline.user_config.init!
          puts "==> Created user config at #{Platform.config_file}"
        end

        project = options[:project] || File.basename(Dir.pwd)
        template_db = options[:template_db] || "#{project}_development"

        content = <<~YAML
          project: #{project}

          # Environment file configuration
          # target: file written in the worktree (e.g. .env.local, .env.development.local, .env)
          # source: file copied from main repo as a starting point
          env_file:
            target: .env.local
            source: .env.local

          # Number of ports to allocate per worktree (e.g. 2 for app + esbuild reload)
          # ports_needed: 1

          database:
            adapter: postgresql
            template: #{template_db}
            pattern: "{template}_{worktree}"

          copy_files:
            - config/master.key

          env:
            PORT: "{port}"
            DATABASE_NAME: "{database}"
            REDIS_URL: "{redis_url}"
            APPLICATION_HOST: "localhost:{port}"

          setup_commands:
            - bundle install --quiet
            # - yarn install --silent

          editor:
            vscode_title: '{project} (:{port}) — {branch} — ${activeEditorShort}'
        YAML

        File.write(path, content)
        puts "==> Created #{PROJECT_CONFIG_FILE} for project '#{project}'"
        puts ""
        puts "Allocation policy (ports, Redis) is managed in your user config:"
        puts "  #{Platform.config_file}"

        open_in_editor(path)
      end

      desc "config", "Show or initialize user-level config"
      def config
        uc = Git::Treeline.user_config
        unless uc.config_file_exists?
          uc.init!
          puts "Created config at #{Platform.config_file}"
          return
        end

        puts "Config: #{Platform.config_file}"
        puts JSON.pretty_generate(uc.data)
      end

      desc "version", "Print version"
      def version
        puts "git-treeline #{VERSION}"
      end

      def self.exit_on_failure?
        true
      end

      private

      def open_in_editor(path)
        editor = ENV.fetch("VISUAL", nil) || ENV.fetch("EDITOR", nil)
        return unless editor

        system(editor, path)
      end
    end
  end
end
