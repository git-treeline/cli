# frozen_string_literal: true

RSpec.describe Git::Treeline do
  it "has a version number" do
    expect(Git::Treeline::VERSION).not_to be_nil
  end

  describe ".detect_project_root" do
    it "returns directory containing .git" do
      Dir.mktmpdir do |dir|
        real_dir = File.realpath(dir)
        FileUtils.mkdir_p(File.join(real_dir, ".git"))
        Dir.chdir(real_dir) do
          expect(Git::Treeline.detect_project_root).to eq(real_dir)
        end
      end
    end

    it "returns directory containing .treeline.yml" do
      Dir.mktmpdir do |dir|
        real_dir = File.realpath(dir)
        File.write(File.join(real_dir, ".treeline.yml"), "project: test")
        Dir.chdir(real_dir) do
          expect(Git::Treeline.detect_project_root).to eq(real_dir)
        end
      end
    end
  end

  describe ".reset!" do
    it "clears memoized singletons" do
      Git::Treeline.user_config
      Git::Treeline.registry
      Git::Treeline.reset!

      expect(Git::Treeline.instance_variable_get(:@user_config)).to be_nil
      expect(Git::Treeline.instance_variable_get(:@registry)).to be_nil
    end
  end
end
