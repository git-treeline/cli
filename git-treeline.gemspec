# frozen_string_literal: true

require_relative "lib/git/treeline/version"

Gem::Specification.new do |spec|
  spec.name = "git-treeline"
  spec.version = Git::Treeline::VERSION
  spec.authors = ["Jonathan Simmons"]
  spec.email = ["jonathan@productmatter.co"]

  spec.summary = "Worktree environment manager — ports, databases, and Redis across parallel development environments."
  spec.description = "Git Treeline manages resource allocation (ports, PostgreSQL databases, Redis) " \
                     "across git worktrees so multiple branches can run simultaneously without collisions. " \
                     "A central registry at ~/.git-treeline/ tracks allocations across all projects."
  spec.homepage = "https://git-treeline.dev"
  spec.license = "Proprietary"
  spec.required_ruby_version = ">= 3.2.0"

  spec.metadata["homepage_uri"] = spec.homepage
  spec.metadata["source_code_uri"] = "https://github.com/product-matter/git-treeline"
  spec.metadata["changelog_uri"] = "https://github.com/product-matter/git-treeline/blob/main/CHANGELOG.md"
  spec.metadata["rubygems_mfa_required"] = "true"

  gemspec = File.basename(__FILE__)
  spec.files = IO.popen(%w[git ls-files -z], chdir: __dir__, err: IO::NULL) do |ls|
    ls.readlines("\x0", chomp: true).reject do |f|
      (f == gemspec) ||
        f.start_with?(*%w[bin/ Gemfile .gitignore .rspec spec/ .github/ .rubocop.yml])
    end
  end
  spec.bindir = "exe"
  spec.executables = spec.files.grep(%r{\Aexe/}) { |f| File.basename(f) }
  spec.require_paths = ["lib"]

  spec.add_dependency "thor", "~> 1.0"
end
